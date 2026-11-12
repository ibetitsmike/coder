-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

CREATE TABLE chat_family_messages (
    id uuid PRIMARY KEY,
    sender_chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    recipient_chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL
);

CREATE INDEX chat_family_messages_sender_created_at_idx ON chat_family_messages (sender_chat_id, created_at);

COMMENT ON TABLE chat_family_messages IS 'Messages that a chat sent to another chat in its family with message_family_chat. The per-chat rate limit counts them.';
