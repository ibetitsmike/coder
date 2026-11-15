-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1df49-3a7d-79d0-ba16-5537b0bacfbe_chat_wakeup_watches

-- Moves the events of every events wake-up into watch rows: one row per
-- entry, in entry order. The run keys that recorded what an entry already
-- sent become delivered_at, deliveries, the output cursor of a process and
-- failed_at. Completed and canceled wake-ups get their watches too, so the
-- wake-up list still shows what they watched; the watch queries read only
-- active wake-ups. An entry that names no target, an unknown type or a
-- child chat that no longer exists has nothing to wait for and is left out.
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
    delivered_at,
    deliveries,
    failed_at,
    created_at
)
SELECT
    gen_random_uuid(),
    e.wakeup_id,
    e.chat_id,
    e.organization_id,
    e.kind,
    e.ordinal,
    e.child_chat_id,
    e.agent_id,
    e.process_id,
    e.pull_request_url,
    (e.entry - ARRAY['type', 'chat_id', 'agent_id', 'process_id', 'url'])
        || CASE WHEN o.cursor > 0 THEN jsonb_build_object('output_cursor', o.cursor) ELSE '{}'::jsonb END,
    d.delivered_at,
    o.sent + CASE WHEN d.delivered_at IS NULL THEN 0 ELSE 1 END,
    CASE WHEN d.delivered_at IS NULL THEN f.failed_at END,
    e.created_at
FROM (
    SELECT
        w.id AS wakeup_id,
        w.chat_id,
        w.organization_id,
        w.created_at,
        x.entry,
        (x.n - 1)::integer AS ordinal,
        CASE x.entry->>'type'
            WHEN 'child_done' THEN 'child_done'::chat_wakeup_watch_kind
            WHEN 'process_exit' THEN 'process'::chat_wakeup_watch_kind
            WHEN 'pull_request' THEN 'pull_request'::chat_wakeup_watch_kind
        END AS kind,
        CASE WHEN x.entry->>'type' = 'child_done'
            AND x.entry->>'chat_id' ~* '^[0-9a-f]{8}-([0-9a-f]{4}-){3}[0-9a-f]{12}$'
            THEN (x.entry->>'chat_id')::uuid
        END AS child_chat_id,
        CASE WHEN x.entry->>'type' = 'process_exit'
            AND x.entry->>'agent_id' ~* '^[0-9a-f]{8}-([0-9a-f]{4}-){3}[0-9a-f]{12}$'
            THEN (x.entry->>'agent_id')::uuid
        END AS agent_id,
        CASE WHEN x.entry->>'type' = 'process_exit' THEN NULLIF(x.entry->>'process_id', '') END AS process_id,
        CASE WHEN x.entry->>'type' = 'pull_request' THEN NULLIF(x.entry->>'url', '') END AS pull_request_url
    FROM chat_wakeups w
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(w.events) = 'array' THEN w.events ELSE '[]'::jsonb END
    ) WITH ORDINALITY AS x(entry, n)
    WHERE w.kind = 'events'
        AND jsonb_typeof(x.entry) = 'object'
) e
-- The key of the run that delivered the entry, and of its failed sends.
CROSS JOIN LATERAL (
    SELECT CASE e.kind
        WHEN 'child_done' THEN 'child_done:' || e.child_chat_id::text
        WHEN 'process' THEN 'process_exit:' || e.process_id
        WHEN 'pull_request' THEN 'pull_request:' || e.pull_request_url
    END AS run_key
) k
LEFT JOIN LATERAL (
    SELECT MIN(r.created_at) AS delivered_at
    FROM chat_wakeup_runs r
    WHERE r.wakeup_id = e.wakeup_id AND r.run_key = k.run_key
) d ON true
LEFT JOIN LATERAL (
    SELECT MIN(r.created_at) AS failed_at
    FROM chat_wakeup_runs r
    WHERE r.wakeup_id = e.wakeup_id AND r.run_key = k.run_key || ':failed'
) f ON true
-- A process with an output pattern sent one message per batch of lines,
-- keyed by the output position after the batch. Failed sends end in
-- ':failed' and do not count.
LEFT JOIN LATERAL (
    SELECT
        COUNT(*)::integer AS sent,
        COALESCE(MAX(substr(r.run_key, length(p.prefix) + 1)::bigint), 0) AS cursor
    FROM chat_wakeup_runs r
    CROSS JOIN (SELECT 'process_output:' || e.process_id || ':' AS prefix) p
    WHERE e.kind = 'process'
        AND r.wakeup_id = e.wakeup_id
        AND left(r.run_key, length(p.prefix)) = p.prefix
        AND substr(r.run_key, length(p.prefix) + 1) ~ '^[0-9]{1,18}$'
) o ON true
WHERE e.kind IS NOT NULL
    AND (e.kind <> 'child_done' OR EXISTS (SELECT 1 FROM chats c WHERE c.id = e.child_chat_id))
    AND (e.kind <> 'process' OR e.process_id IS NOT NULL)
    AND (e.kind <> 'pull_request' OR e.pull_request_url IS NOT NULL);

ALTER TABLE chat_wakeups DROP COLUMN events;
