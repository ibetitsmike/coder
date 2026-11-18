-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1e7a0-b6c0-7429-bb01-13b604e2c875_workflow_runs_retention_index
-- requires: 01a1e3a6-abb4-79d2-8ab8-1669a9415507_workflow_run_detail2

-- A running workflow run belongs to one chatd process. owner_id is the
-- replica ID of the process that executes the script, and heartbeat_at is
-- the last time that process said it still runs it, on the clock of chatd.
-- A run whose heartbeat is older than the stale window has lost its owner,
-- so the sweep marks it interrupted and a resume on any replica may claim
-- it. Both stay NULL on rows written before this node; the sweep treats a
-- NULL heartbeat as stale.
ALTER TABLE workflow_runs
    ADD COLUMN owner_id uuid,
    ADD COLUMN heartbeat_at timestamptz;
