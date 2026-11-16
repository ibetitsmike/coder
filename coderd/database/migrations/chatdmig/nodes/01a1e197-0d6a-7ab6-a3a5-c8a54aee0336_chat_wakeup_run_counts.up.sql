-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1e008-494e-723d-8aaf-f0e13a811456_chat_wakeup_ready_watches

-- The delivered wake-up runs of each chat per UTC hour, which the daily run
-- caps sum over the last 24 hours. The delivery increments its hour in the
-- transaction of its message. A row outlives the wake-ups that counted in it,
-- and dbpurge deletes the rows older than 7 days.
CREATE TABLE chat_wakeup_run_counts (
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    -- The start of the UTC hour.
    hour timestamptz NOT NULL,
    -- The organization of the chat, copied from the wake-up.
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    runs integer NOT NULL CHECK (runs >= 1),
    PRIMARY KEY (chat_id, hour)
);
CREATE INDEX chat_wakeup_run_counts_org_hour_idx ON chat_wakeup_run_counts (organization_id, hour) INCLUDE (runs);

-- The runs delivered before this node keep counting for the rest of their
-- window.
INSERT INTO chat_wakeup_run_counts (chat_id, hour, organization_id, runs)
SELECT chat_id, date_trunc('hour', created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC', organization_id, COUNT(*)
FROM chat_wakeup_runs
WHERE status = 'delivered'
    AND created_at >= now() - interval '25 hours'
GROUP BY 1, 2, 3;
