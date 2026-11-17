-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1e197-0d6a-7ab6-a3a5-c8a54aee0336_chat_wakeup_run_counts
-- transaction: none

-- The wake-ups that dbpurge may delete, oldest first, so a purge batch reads
-- only them instead of scanning the table. The statuses match
-- DeleteOldChatWakeups. CONCURRENTLY keeps writes going during the build.
CREATE INDEX CONCURRENTLY IF NOT EXISTS chat_wakeups_retention_idx ON chat_wakeups (updated_at)
    WHERE status IN ('completed', 'canceled');
