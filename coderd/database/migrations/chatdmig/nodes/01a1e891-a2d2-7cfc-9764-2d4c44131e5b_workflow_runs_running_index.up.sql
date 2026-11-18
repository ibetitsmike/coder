-- requires: 01a1e891-6025-7d4e-b4bc-bf9b42beabf2_workflow_run_owner
-- transaction: none

-- The running runs by heartbeat, so the sweep that interrupts the runs of a
-- dead owner reads only the open rows instead of scanning the table, which
-- keeps every terminal run for 30 days. CONCURRENTLY keeps writes going
-- during the build.
CREATE INDEX CONCURRENTLY IF NOT EXISTS workflow_runs_running_idx ON workflow_runs (heartbeat_at)
    WHERE status = 'running';
