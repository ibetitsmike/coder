-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1bd31-6117-7cbd-bb14-1a8ca7a596b3_chat_subagents

-- The todo list of a chat (todo_write replaces it as a whole).
CREATE TABLE chat_todos (
    chat_id uuid PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    -- JSON array of {"id", "content", "status"} objects in list order.
    items jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- Starts at 1 and grows by one on every write, so readers can order events.
    version bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now()
);
