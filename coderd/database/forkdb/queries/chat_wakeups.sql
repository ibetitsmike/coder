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
    events,
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
    @events,
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

-- Locks the wake-up until the transaction ends, so a worker that finishes its
-- claim in between waits and then finds its lease gone.
-- name: GetChatWakeupByIDForUpdate :one
SELECT * FROM chat_wakeups WHERE id = @id FOR UPDATE;

-- name: ListChatWakeupsByChatID :many
SELECT * FROM chat_wakeups WHERE chat_id = @chat_id ORDER BY created_at, id;

-- name: ListChildChatIDsWithActiveEventsWakeups :many
-- Lists the children of @parent_chat_id that wait for events of their own,
-- so a caller checks every child in one read.
SELECT DISTINCT w.chat_id
FROM chat_wakeups w
JOIN chats c ON c.id = w.chat_id
WHERE c.parent_chat_id = @parent_chat_id::uuid
    AND w.kind = 'events'
    AND w.status = 'active';

-- Pauses, resumes, cancels or completes a wake-up. The caller computes
-- next_run_at. Any claim on the row ends.
-- name: UpdateChatWakeupStatus :one
UPDATE chat_wakeups
SET
    status = @status,
    status_reason = @status_reason,
    next_run_at = @next_run_at,
    lease_expires_at = NULL,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: CancelChatWakeupsByChatID :many
UPDATE chat_wakeups
SET
    status = 'canceled',
    status_reason = @reason::text,
    next_run_at = NULL,
    lease_expires_at = NULL,
    updated_at = @updated_at
WHERE chat_id = @chat_id
    AND status IN ('active', 'paused')
RETURNING *;

-- Claims due wake-ups until lease_expires_at. SKIP LOCKED keeps concurrent
-- workers from blocking on, or claiming, the same rows. Rows of chats that
-- would queue a new message stay unclaimed, so they cannot fill the batch and
-- starve the rows of idle chats. Those are the busy and paused chats, and the
-- errored chats with a non-empty queue (chatstate's R, I, A, P and E1 states).
-- Archived chats are always claimed so the worker cancels their wake-ups.
-- name: ClaimDueChatWakeups :many
UPDATE chat_wakeups
SET
    lease_expires_at = @lease_expires_at::timestamptz,
    updated_at = @now::timestamptz
WHERE id IN (
    SELECT cw.id
    FROM chat_wakeups cw
    WHERE cw.status = 'active'
        AND cw.next_run_at <= @now::timestamptz
        AND (cw.lease_expires_at IS NULL OR cw.lease_expires_at <= @now::timestamptz)
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
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- Records a run of a claimed wake-up and ends the claim. Pass the
-- lease_expires_at that ClaimDueChatWakeups returned: no row means another
-- worker took over the expired lease.
-- name: FinishChatWakeupClaim :one
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
    AND lease_expires_at = @lease_expires_at::timestamptz
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

-- name: ListChatWakeupRunKeys :many
-- A failed send of an events entry records its run under a key of its own,
-- so it never blocks a later run of the entry.
SELECT run_key FROM chat_wakeup_runs WHERE wakeup_id = @wakeup_id::uuid ORDER BY created_at, id;

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
