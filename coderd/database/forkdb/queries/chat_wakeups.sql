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

-- name: ListChatWakeupsByChatID :many
SELECT * FROM chat_wakeups WHERE chat_id = @chat_id ORDER BY created_at, id;

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

-- name: DeleteChatWakeupByID :exec
DELETE FROM chat_wakeups WHERE id = @id;

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
-- workers from blocking on, or claiming, the same rows. Rows of busy chats stay
-- unclaimed, so they cannot fill the batch and starve the rows of idle chats.
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
                AND c.status IN ('running', 'interrupting', 'requires_action')
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

-- name: UpdateChatWakeupRunStatus :one
UPDATE chat_wakeup_runs
SET
    status = @status,
    error = @error,
    updated_at = @updated_at
WHERE id = @id
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
