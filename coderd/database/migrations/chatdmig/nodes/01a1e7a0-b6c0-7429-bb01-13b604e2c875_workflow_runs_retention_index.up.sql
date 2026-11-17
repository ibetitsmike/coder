-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1d05b-a4aa-7050-ac85-2f3c1e806fe9_workflow_run_timestamps
-- transaction: none

-- The workflow runs that dbpurge may delete, oldest first, so a purge batch
-- reads only them instead of scanning the table. The statuses match
-- DeleteOldWorkflowRuns. CONCURRENTLY keeps writes going during the build.
CREATE INDEX CONCURRENTLY IF NOT EXISTS workflow_runs_retention_idx ON workflow_runs (updated_at)
    WHERE status IN ('completed', 'failed', 'stopped', 'interrupted');
