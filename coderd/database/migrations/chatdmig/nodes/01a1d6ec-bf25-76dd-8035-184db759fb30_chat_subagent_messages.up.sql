-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

CREATE TABLE chat_subagent_messages (
    -- The tool call ID of the message_agent call that sent the message.
    id uuid PRIMARY KEY,
    sender_chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    recipient_chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL
);

CREATE INDEX chat_subagent_messages_sender_created_at_idx ON chat_subagent_messages (sender_chat_id, created_at);
CREATE INDEX chat_subagent_messages_recipient_chat_id_idx ON chat_subagent_messages (recipient_chat_id);

COMMENT ON TABLE chat_subagent_messages IS 'Messages that a chat sent to a child with message_agent, so that a replayed call does not send its message again.';
