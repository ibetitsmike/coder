-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1d05b-a4aa-7050-ac85-2f3c1e806fe9_workflow_run_timestamps
-- requires: 01a1e197-0d6a-7ab6-a3a5-c8a54aee0336_chat_wakeup_run_counts

-- The rows that dbpurge may delete, oldest first, so a purge batch reads only
-- them instead of scanning the table. The statuses match DeleteOldChatWakeups
-- and DeleteOldWorkflowRuns.
CREATE INDEX chat_wakeups_retention_idx ON chat_wakeups (updated_at)
    WHERE status IN ('completed', 'canceled');
CREATE INDEX workflow_runs_retention_idx ON workflow_runs (updated_at)
    WHERE status IN ('completed', 'failed', 'stopped', 'interrupted');
