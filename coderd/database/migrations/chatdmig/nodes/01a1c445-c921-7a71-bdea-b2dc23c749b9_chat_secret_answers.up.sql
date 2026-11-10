-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1c256-cefe-797e-8c6a-1c0558040659_chat_descendant_questions

CREATE TABLE chat_secret_answers (
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    tool_call_id text NOT NULL,
    value text NOT NULL,
    value_key_id text REFERENCES dbcrypt_keys(active_key_digest),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, tool_call_id)
);

COMMENT ON TABLE chat_secret_answers IS 'Answers the user gave to secret ask_user_question calls of descendant chats. Tools of the asking chat read them; they are never written to chat messages.';
COMMENT ON COLUMN chat_secret_answers.value IS 'Encrypted by dbcrypt when value_key_id is set.';
