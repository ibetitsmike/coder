-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1e008-494e-723d-8aaf-f0e13a811456_chat_wakeup_ready_watches

-- The delivered wake-up runs of each chat per UTC hour, which the daily run
-- caps sum over the last 24 hours. A trigger on chat_wakeup_runs counts each
-- delivered run in the transaction that inserts it, so a replica that does not
-- know this table still counts its deliveries. A row outlives the wake-ups that
-- counted in it.
CREATE TABLE chat_wakeup_run_counts (
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    -- The start of the UTC hour.
    hour timestamptz NOT NULL,
    -- The organization of the chat, copied from the run.
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    runs integer NOT NULL CHECK (runs >= 1),
    PRIMARY KEY (chat_id, hour)
);
CREATE INDEX chat_wakeup_run_counts_org_hour_idx ON chat_wakeup_run_counts (organization_id, hour) INCLUDE (runs);

CREATE FUNCTION count_delivered_chat_wakeup_run() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    INSERT INTO chat_wakeup_run_counts (chat_id, hour, organization_id, runs)
    VALUES (NEW.chat_id, date_trunc('hour', NEW.created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC', NEW.organization_id, 1)
    ON CONFLICT (chat_id, hour) DO UPDATE SET runs = chat_wakeup_run_counts.runs + 1;
    RETURN NULL;
END;
$$;

-- Creating the trigger locks chat_wakeup_runs against inserts until this node
-- commits, so every run is counted once: by the backfill below or by the
-- trigger.
CREATE TRIGGER trigger_count_delivered_chat_wakeup_run AFTER INSERT ON chat_wakeup_runs
    FOR EACH ROW WHEN (NEW.status = 'delivered') EXECUTE FUNCTION count_delivered_chat_wakeup_run();

-- The runs delivered before this node keep counting for the rest of their
-- window.
INSERT INTO chat_wakeup_run_counts (chat_id, hour, organization_id, runs)
SELECT chat_id, date_trunc('hour', created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC', organization_id, COUNT(*)
FROM chat_wakeup_runs
WHERE status = 'delivered'
    AND created_at >= now() - interval '25 hours'
GROUP BY 1, 2, 3;
