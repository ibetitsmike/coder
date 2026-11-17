-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1df49-3a7d-79d0-ba16-5537b0bacfbe_chat_wakeup_watches

-- The open process watches by their process. A workspace agent reports the
-- exits and output matches of its processes, and FireChatProcessWatches
-- finds the watches of each report through this index.
CREATE INDEX chat_wakeup_watches_open_process_idx ON chat_wakeup_watches (agent_id, process_id)
WHERE kind = 'process' AND delivered_at IS NULL;
