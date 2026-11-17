-- The chat and organization come from the wake-up. Returns no row when the
-- wake-up is gone.
-- name: InsertChatWakeupWatch :one
INSERT INTO chat_wakeup_watches (
    id,
    wakeup_id,
    chat_id,
    organization_id,
    kind,
    ordinal,
    child_chat_id,
    agent_id,
    process_id,
    pull_request_url,
    spec,
    created_at
)
SELECT
    @id,
    w.id,
    w.chat_id,
    w.organization_id,
    @kind,
    @ordinal,
    @child_chat_id,
    @agent_id,
    @process_id,
    @pull_request_url,
    @spec,
    @created_at
FROM chat_wakeups w
WHERE w.id = @wakeup_id::uuid
RETURNING *;

-- name: ListChatWakeupWatchesByWakeupID :many
SELECT * FROM chat_wakeup_watches WHERE wakeup_id = @wakeup_id::uuid ORDER BY ordinal;

-- name: ListChatWakeupWatchesByChatID :many
SELECT * FROM chat_wakeup_watches WHERE chat_id = @chat_id::uuid ORDER BY wakeup_id, ordinal;

-- Lists the watches of the active wake-ups of @chat_id that are not
-- delivered, so a new watch can leave out what an earlier one still watches.
-- name: ListOpenChatWakeupWatchesByChatID :many
SELECT sqlc.embed(x), w.expires_at AS wakeup_expires_at
FROM chat_wakeup_watches x
JOIN chat_wakeups w ON w.id = x.wakeup_id
WHERE x.chat_id = @chat_id::uuid
    AND x.delivered_at IS NULL
    AND w.status = 'active'
ORDER BY w.created_at, w.id, x.ordinal;

-- Lists the children of @parent_chat_id that wait for events of their own:
-- they have an open watch on an active wake-up. chat_wakeup_ready_watches
-- uses the same rule.
-- name: ListChildChatIDsWithOpenWakeupWatches :many
SELECT DISTINCT x.chat_id
FROM chat_wakeup_watches x
JOIN chat_wakeups w ON w.id = x.wakeup_id
JOIN chats c ON c.id = x.chat_id
WHERE c.parent_chat_id = @parent_chat_id::uuid
    AND x.delivered_at IS NULL
    AND w.status = 'active';

-- Lists the watches in chat_wakeup_ready_watches: open watches of active
-- wake-ups that have something to deliver to an idle chat. @chat_id and
-- @wakeup_id narrow the list when set.
-- name: ListReadyChatWakeupWatches :many
SELECT x.*
FROM chat_wakeup_watches x
WHERE x.id IN (
        SELECT r.id
        FROM chat_wakeup_ready_watches r
        WHERE (sqlc.narg('chat_id')::uuid IS NULL OR r.chat_id = sqlc.narg('chat_id')::uuid)
            AND (sqlc.narg('wakeup_id')::uuid IS NULL OR r.wakeup_id = sqlc.narg('wakeup_id')::uuid)
    )
ORDER BY x.wakeup_id, x.ordinal
LIMIT @limit_count::int;

-- Records a message that the watch sent. Run it in the transaction that
-- inserts the message, after the wake-up row is locked. @deliveries is the
-- count the caller read: no row means another delivery of the watch won,
-- or the watch is done. @done marks the watch delivered, and @spec_patch
-- merges into its spec. A delivery consumes the fired event, so an open
-- watch is not ready again until a new event fires. @fired_payload, when set,
-- is the payload the caller read: no row means that a later event changed
-- it.
-- name: MarkChatWakeupWatchDelivered :one
UPDATE chat_wakeup_watches
SET
    deliveries = deliveries + 1,
    delivered_at = CASE WHEN @done::boolean THEN @now::timestamptz ELSE NULL END,
    spec = spec || @spec_patch::jsonb,
    fired_at = NULL,
    fired_payload = NULL,
    failed_at = NULL
WHERE id = @id::uuid
    AND wakeup_id = @wakeup_id::uuid
    AND delivered_at IS NULL
    AND deliveries = @deliveries::int
    AND (sqlc.narg('fired_payload')::jsonb IS NULL OR fired_payload = sqlc.narg('fired_payload')::jsonb)
RETURNING *;

-- Fires the open process watches of active wake-ups on process @process_id
-- of chat @chat_id on agent @agent_id, for an event that the agent reported
-- or that chatd read from it. @wakeup_id narrows the watches when set. An
-- event that ends the watch, @ended, is an object with an exit or a lost key,
-- which merges into the payload. Otherwise the event carries @matched output
-- lines that matched the pattern of the watch, @lines, which add to the lines
-- the payload holds until a delivery, keeping the last 16384 characters, and
-- @last, set on the last report of the agent. Lines fire only a watch with an
-- output pattern whose message count is below its output_max_events, and
-- that has not ended. The first event keeps its fired_at. Returns the chats
-- to poke.
-- name: FireChatProcessWatches :many
UPDATE chat_wakeup_watches x
SET
    fired_at = COALESCE(x.fired_at, @fired_at::timestamptz),
    fired_payload = CASE
        WHEN sqlc.narg('ended')::jsonb IS NOT NULL THEN
            COALESCE(x.fired_payload, '{}'::jsonb) || sqlc.narg('ended')::jsonb
        ELSE
            COALESCE(x.fired_payload, '{}'::jsonb) || jsonb_build_object(
                'matched', COALESCE((x.fired_payload->>'matched')::int, 0) + @matched::int,
                'lines', right(concat_ws(E'\n', NULLIF(x.fired_payload->>'lines', ''), @lines::text), 16384),
                'last', COALESCE((x.fired_payload->>'last')::boolean, false) OR @last::boolean
            )
        END
FROM chat_wakeups w
WHERE w.id = x.wakeup_id
    AND w.status = 'active'
    AND x.kind = 'process'
    AND x.delivered_at IS NULL
    AND x.chat_id = @chat_id::uuid
    AND x.agent_id = @agent_id::uuid
    AND x.process_id = @process_id::text
    AND (sqlc.narg('wakeup_id')::uuid IS NULL OR x.wakeup_id = sqlc.narg('wakeup_id')::uuid)
    AND (
        sqlc.narg('ended')::jsonb IS NOT NULL
        OR (
            COALESCE(x.spec->>'output_pattern', '') <> ''
            AND x.deliveries < COALESCE((x.spec->>'output_max_events')::int, 0)
            AND x.fired_payload->'exit' IS NULL
            AND x.fired_payload->'lost' IS NULL
        )
    )
RETURNING x.chat_id;

-- Fires as lost the open process watches of active wake-ups whose agent can
-- no longer report the process. With @reason when the agent is not an agent
-- of the latest build of the chat's workspace, or that build does not start
-- the workspace, or the chat has no workspace; the time of the loss is when
-- the latest build was created, or @now without one. With @restarted_reason
-- when the agent is current but started after the watch was created, which
-- an agent reports only when its process starts, so it lost the processes
-- of its earlier run; the time of the loss is when the agent started.
-- Watches that already ended stay as they are. Returns the chats to poke.
-- name: MarkLostChatProcessWatches :many
WITH lost AS (
    SELECT
        x.id,
        CASE WHEN ag.id IS NULL
            THEN @reason::text
            ELSE @restarted_reason::text
        END AS reason,
        CASE WHEN ag.id IS NULL
            THEN COALESCE(lb.created_at, @now::timestamptz)
            ELSE ag.started_at
        END AS lost_at
    FROM chat_wakeup_watches x
    JOIN chat_wakeups w ON w.id = x.wakeup_id
    JOIN chats c ON c.id = x.chat_id
    LEFT JOIN LATERAL (
        SELECT b.job_id, b.transition, b.created_at
        FROM workspace_builds b
        WHERE b.workspace_id = c.workspace_id
        ORDER BY b.build_number DESC
        LIMIT 1
    ) lb ON true
    LEFT JOIN LATERAL (
        SELECT a.id, a.started_at
        FROM workspace_agents a
        JOIN workspace_resources r ON r.id = a.resource_id
        WHERE lb.transition = 'start'
            AND a.id = x.agent_id
            AND r.job_id = lb.job_id
            AND NOT a.deleted
    ) ag ON true
    WHERE x.kind = 'process'
        AND x.delivered_at IS NULL
        AND w.status = 'active'
        AND x.fired_payload->'exit' IS NULL
        AND x.fired_payload->'lost' IS NULL
        AND (ag.id IS NULL OR ag.started_at > x.created_at)
)
UPDATE chat_wakeup_watches x
SET
    fired_at = COALESCE(x.fired_at, @now::timestamptz),
    fired_payload = COALESCE(x.fired_payload, '{}'::jsonb) || jsonb_build_object(
        'lost', jsonb_build_object('reason', lost.reason, 'at', lost.lost_at)
    )
FROM lost
WHERE x.id = lost.id
    AND x.delivered_at IS NULL
RETURNING x.chat_id;

-- Claims the open pull_request watches that the pull request poller of a
-- replica reads next: those of active wake-ups of unarchived chats whose
-- spec next_poll_at has come by @now, or that have none, and that no read
-- covered through the expiry of their wake-up yet. A watch that fired is
-- read on until its delivery, so the text it sends covers the activity up
-- to its last read. The claim moves next_poll_at to @lease_until, so the
-- poller of another replica skips the watch until the claimer records its
-- read or the lease runs out. The rows are sorted by pull request and chat
-- owner, the key of one shared read.
-- name: ClaimDuePullRequestWatches :many
WITH due AS (
    SELECT x.id, c.owner_id, w.expires_at
    FROM chat_wakeup_watches x
    JOIN chat_wakeups w ON w.id = x.wakeup_id
    JOIN chats c ON c.id = x.chat_id
    WHERE x.kind = 'pull_request'
        AND x.delivered_at IS NULL
        AND w.status = 'active'
        AND NOT c.archived
        AND COALESCE((x.spec->>'next_poll_at')::timestamptz, '-infinity') <= @now::timestamptz
        AND (w.expires_at IS NULL OR COALESCE((x.spec->>'polled_at')::timestamptz, '-infinity') < w.expires_at)
    ORDER BY x.pull_request_url, c.owner_id, x.id
    LIMIT @limit_count::int
    FOR UPDATE OF x SKIP LOCKED
),
claimed AS (
    UPDATE chat_wakeup_watches x
    SET spec = x.spec || jsonb_build_object('next_poll_at', @lease_until::timestamptz)
    FROM due
    WHERE x.id = due.id
    RETURNING x.id, x.wakeup_id, x.chat_id, x.pull_request_url, x.spec, x.fired_at
)
SELECT claimed.id, claimed.wakeup_id, claimed.chat_id, claimed.pull_request_url, claimed.spec,
    claimed.fired_at, due.owner_id, due.expires_at AS wakeup_expires_at
FROM claimed
JOIN due ON due.id = claimed.id
ORDER BY claimed.pull_request_url, due.owner_id, claimed.id;

-- Records a read of the poller: merges @spec_patch into the spec of the
-- open pull_request watch, and with @fired_payload also stores the text of
-- the read. The first such read fires the watch at @fired_at, which makes it
-- ready (chat_wakeup_ready_watches), and a later one keeps that time and
-- replaces the text. No row changes when the watch is delivered.
-- name: RecordPullRequestWatchPoll :execrows
UPDATE chat_wakeup_watches
SET
    spec = spec || @spec_patch::jsonb,
    fired_at = COALESCE(fired_at, sqlc.narg('fired_at')::timestamptz),
    fired_payload = COALESCE(sqlc.narg('fired_payload')::jsonb, fired_payload)
WHERE id = @id::uuid
    AND wakeup_id = @wakeup_id::uuid
    AND kind = 'pull_request'
    AND delivered_at IS NULL;

-- Records that a send of the watch failed. The first failure since the last
-- delivery keeps its time.
-- name: MarkChatWakeupWatchFailed :exec
UPDATE chat_wakeup_watches
SET failed_at = COALESCE(failed_at, @now::timestamptz)
WHERE id = @id::uuid
    AND wakeup_id = @wakeup_id::uuid
    AND delivered_at IS NULL;
