-- requires: 01a1d6ec-bf25-76dd-8035-184db759fb30_chat_subagent_messages

CREATE TABLE chat_notes (
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    path text NOT NULL CHECK (path ~ '^[a-z0-9][a-z0-9._/-]{0,127}$'),
    content text NOT NULL CHECK (octet_length(content) <= 16384),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, path)
);

COMMENT ON TABLE chat_notes IS 'Notes that a chat keeps with the notes tool, such as the checkpoint.md of the fork-token-budget experiment. They outlast compaction and new context windows.';
