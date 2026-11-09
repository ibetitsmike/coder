-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1bd31-6117-7cbd-bb14-1a8ca7a596b3_chat_subagents

-- The custom agent's tool policy, snapshotted at spawn. NULL means no policy.
ALTER TABLE chat_subagents ADD COLUMN tool_policy jsonb;
