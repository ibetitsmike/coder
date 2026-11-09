-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1c18d-027a-7d4a-a2ad-e823d1e3ca6c_agent_definitions

CREATE TABLE chat_descendant_questions (
    chat_id uuid PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    tool_call_id text NOT NULL,
    questions jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

COMMENT ON TABLE chat_descendant_questions IS 'The latest question each descendant chat asked the user with ask_user_question. The question is pending until a user message reaches that chat after created_at.';
