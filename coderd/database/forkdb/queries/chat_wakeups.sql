-- The organization comes from the chat.
-- name: InsertChatWakeup :one
INSERT INTO chat_wakeups (
    id,
    chat_id,
    organization_id,
    created_by,
    source,
    kind,
    prompt,
    interval_seconds,
    cron,
    timezone,
    status,
    status_reason,
    next_run_at,
    expires_at,
    max_runs,
    created_at,
    updated_at
)
SELECT
    @id,
    c.id,
    c.organization_id,
    @created_by,
    @source,
    @kind,
    @prompt,
    @interval_seconds,
    @cron,
    @timezone,
    @status,
    @status_reason,
    @next_run_at,
    @expires_at,
    @max_runs,
    @created_at,
    @updated_at
FROM chats c
WHERE c.id = @chat_id::uuid
RETURNING *;

-- name: GetChatWakeupByID :one
SELECT * FROM chat_wakeups WHERE id = @id;

-- Locks the wake-up until the transaction ends, so a delivery that starts in
-- between skips it.
-- name: GetChatWakeupByIDForUpdate :one
SELECT * FROM chat_wakeups WHERE id = @id FOR UPDATE;

-- name: ListChatWakeupsByChatID :many
SELECT * FROM chat_wakeups WHERE chat_id = @chat_id ORDER BY created_at, id;

-- Pauses, resumes, cancels or completes a wake-up. The caller computes
-- next_run_at.
-- name: UpdateChatWakeupStatus :one
UPDATE chat_wakeups
SET
    status = @status,
    status_reason = @status_reason,
    next_run_at = @next_run_at,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: CancelChatWakeupsByChatID :many
UPDATE chat_wakeups
SET
    status = 'canceled',
    status_reason = @reason::text,
    next_run_at = NULL,
    updated_at = @updated_at
WHERE chat_id = @chat_id
    AND status IN ('active', 'paused')
RETURNING *;

-- Lists, in one statement, the due wake-ups and the earliest active wake-up
-- that is not due, which sets the timer of the waker. It reads and locks
-- nothing: a delivery locks its wake-up with LockActiveChatWakeup. A wake-up
-- is due when its chat would not queue a new message and either its run time
-- has come or, for an events wake-up before its expiry, it has a child_done
-- watch in chat_wakeup_ready_watches whose last send did not fail. A failed
-- send waits for the run time that the failure set, so it does not repeat at
-- once, and the other watches wait for their run time, which reads them. The
-- chats that would queue are the busy and paused chats, and the errored chats
-- with a non-empty queue (chatstate's R, I, A, P and E1 states). The wake-ups
-- of archived chats are always due by their run time, so the waker cancels
-- them. chat_id limits the list to the wake-ups of one chat and to those that
-- watch it as a child. The due rows page by (next_run_at, id) after
-- after_run_at and after_id.
-- name: ListDueChatWakeups :many
WITH candidates AS (
    SELECT cw.id
    FROM chat_wakeups cw
    WHERE cw.status = 'active'
        AND (sqlc.narg('chat_id')::uuid IS NULL OR cw.chat_id = sqlc.narg('chat_id')::uuid)
        AND cw.next_run_at <= @now::timestamptz
    UNION
    SELECT r.wakeup_id
    FROM chat_wakeup_ready_watches r
    JOIN chat_wakeups cw ON cw.id = r.wakeup_id
    WHERE cw.kind = 'events'
        AND (cw.expires_at IS NULL OR cw.expires_at > @now::timestamptz)
        AND r.kind = 'child_done'
        AND r.failed_at IS NULL
        AND (
            sqlc.narg('chat_id')::uuid IS NULL
            OR r.chat_id = sqlc.narg('chat_id')::uuid
            OR r.child_chat_id = sqlc.narg('chat_id')::uuid
        )
),
due AS (
    SELECT cw.id
    FROM candidates d
    JOIN chat_wakeups cw ON cw.id = d.id
    WHERE cw.status = 'active'
        AND (cw.next_run_at, cw.id) > (@after_run_at::timestamptz, @after_id::uuid)
        AND NOT EXISTS (
            SELECT 1
            FROM chats c
            WHERE c.id = cw.chat_id
                AND NOT c.archived
                AND (
                    c.status IN ('running', 'interrupting', 'requires_action', 'paused')
                    OR (
                        c.status = 'error'
                        AND EXISTS (SELECT 1 FROM chat_queued_messages q WHERE q.chat_id = c.id)
                    )
                )
        )
    ORDER BY cw.next_run_at, cw.id
    LIMIT @limit_count::int
),
upcoming AS (
    SELECT cw.id
    FROM chat_wakeups cw
    WHERE cw.status = 'active'
        AND (sqlc.narg('chat_id')::uuid IS NULL OR cw.chat_id = sqlc.narg('chat_id')::uuid)
        AND cw.next_run_at > @now::timestamptz
        AND cw.id NOT IN (SELECT id FROM due)
    ORDER BY cw.next_run_at, cw.id
    LIMIT 1
)
SELECT sqlc.embed(w), l.due::boolean AS due
FROM (
    SELECT id, true AS due FROM due
    UNION ALL
    SELECT id, false AS due FROM upcoming
) l
JOIN chat_wakeups w ON w.id = l.id
ORDER BY w.next_run_at, w.id;

-- Locks an active wake-up for its delivery. SKIP LOCKED returns no row while
-- another transaction holds the wake-up, which then decides it.
-- name: LockActiveChatWakeup :one
SELECT * FROM chat_wakeups WHERE id = @id AND status = 'active' FOR UPDATE SKIP LOCKED;

-- Records how a delivery of a wake-up locked by LockActiveChatWakeup ended.
-- It clears a lease that a replica of an older version took with its claim,
-- so that replica's FinishChatWakeupClaim writes nothing over this result.
-- name: UpdateChatWakeupRun :one
UPDATE chat_wakeups
SET
    next_run_at = @next_run_at,
    status = @status,
    status_reason = @status_reason,
    run_count = run_count + @run_count_delta::int,
    last_run_at = COALESCE(sqlc.narg('last_run_at')::timestamptz, last_run_at),
    lease_expires_at = NULL,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- Returns no row when the run of this occurrence already exists. The chat and
-- organization come from the wake-up, so a run always counts against the
-- chat that owns it.
-- name: InsertChatWakeupRun :one
INSERT INTO chat_wakeup_runs (
    id,
    wakeup_id,
    chat_id,
    organization_id,
    run_key,
    status,
    error,
    created_at,
    updated_at
)
SELECT
    @id,
    w.id,
    w.chat_id,
    w.organization_id,
    @run_key,
    @status,
    @error,
    @created_at,
    @updated_at
FROM chat_wakeups w
WHERE w.id = @wakeup_id::uuid
ON CONFLICT (wakeup_id, run_key) DO NOTHING
RETURNING *;

-- name: CountChatWakeupRunsSince :one
SELECT COUNT(*)
FROM chat_wakeup_runs
WHERE chat_id = @chat_id
    AND status = 'delivered'
    AND created_at >= @since::timestamptz;

-- name: CountOrganizationChatWakeupRunsSince :one
SELECT COUNT(*)
FROM chat_wakeup_runs
WHERE organization_id = @organization_id
    AND status = 'delivered'
    AND created_at >= @since::timestamptz;

-- Deleted messages count too: they were paid for. Messages without a price
-- have a NULL cost and count as zero.
-- name: SumChatCostMicrosSince :one
SELECT COALESCE(SUM(total_cost_micros), 0)::bigint
FROM chat_messages
WHERE chat_id = @chat_id::uuid
    AND created_at >= @since::timestamptz
    AND total_cost_micros IS NOT NULL;

-- Sums the spend of the chats of the organization that have a wake-up, so
-- chats that only humans drive do not count.
-- name: SumOrganizationWakeupChatCostMicrosSince :one
SELECT COALESCE(SUM(cm.total_cost_micros), 0)::bigint
FROM chat_messages cm
WHERE cm.chat_id IN (SELECT DISTINCT w.chat_id FROM chat_wakeups w WHERE w.organization_id = @organization_id::uuid)
    AND cm.created_at >= @since::timestamptz
    AND cm.total_cost_micros IS NOT NULL;

-- Counts the notifications of one template that were enqueued for a user
-- about a chat. One enqueue stores a row per delivery method, all with the
-- same payload, and the enqueuer's dedupe rejects a second identical payload
-- on the same UTC day, so distinct payloads per UTC day count enqueues.
-- name: CountChatUserNotificationsSince :one
SELECT COUNT(DISTINCT (payload, DATE_TRUNC('day', created_at AT TIME ZONE 'UTC')))
FROM notification_messages
WHERE user_id = @user_id::uuid
    AND notification_template_id = @notification_template_id::uuid
    AND payload->'data'->>'chat_id' = @chat_id::text
    AND created_at >= @since::timestamptz;
