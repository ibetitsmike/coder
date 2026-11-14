-- requires: 01a1c256-cefe-797e-8c6a-1c0558040659_chat_descendant_questions
-- requires: 01a1d903-ff52-75e8-91c2-e50d900ac0b7_chat_note_calls
-- requires: 01a1da86-66a9-72e8-b76d-645efa0c0ff0_chat_organization_token_budget

-- Stores whether a descendant question is answered, which reads derived
-- from the chat's later user messages before.
ALTER TABLE chat_descendant_questions
    ADD COLUMN root_chat_id uuid REFERENCES chats(id) ON DELETE CASCADE,
    ADD COLUMN answered_at timestamp with time zone,
    ADD COLUMN answered_by text;

-- A child names its family root in chats.root_chat_id. A chat without one
-- names itself, which the list of a family never shows, as before.
UPDATE chat_descendant_questions q
SET root_chat_id = COALESCE(c.root_chat_id, c.id)
FROM chats c
WHERE c.id = q.chat_id;

-- A question that the rule of the old reads retired is answered. Every
-- other question stays pending, so no child loses the question it waits on.
UPDATE chat_descendant_questions q
SET answered_at = now(),
    answered_by = 'migrated'
WHERE EXISTS (
    SELECT 1
    FROM chat_messages m
    WHERE m.chat_id = q.chat_id
        AND m.role = 'user'
        AND m.created_at > q.created_at
        AND (
            (NOT m.deleted
                AND (
                    m.compressed
                    OR (m.visibility <> 'model'
                        AND jsonb_typeof(q.questions) = 'array'
                        AND NOT jsonb_path_exists(q.questions, '$[*] ? (@.secret == true)'))
                ))
            OR (m.visibility <> 'model'
                AND EXISTS (
                    SELECT 1
                    FROM jsonb_array_elements(CASE WHEN jsonb_typeof(m.content) = 'array' THEN m.content ELSE '[]'::jsonb END) AS part
                    WHERE part->>'type' = 'text'
                        AND starts_with(part->>'text', 'Answer to ask_user_question call ' || q.tool_call_id || ':')
                ))
        )
);

ALTER TABLE chat_descendant_questions
    ALTER COLUMN root_chat_id SET NOT NULL,
    ADD CONSTRAINT chat_descendant_questions_answered_by_check CHECK (
        (answered_at IS NULL AND answered_by IS NULL)
        OR (answered_at IS NOT NULL
            AND answered_by IS NOT NULL
            AND answered_by IN ('message', 'secret_answer', 'context_boundary', 'migrated'))
    );

CREATE INDEX chat_descendant_questions_root_idx
    ON chat_descendant_questions (root_chat_id);

COMMENT ON TABLE chat_descendant_questions IS 'The latest question each descendant chat asked the user with ask_user_question. The question is pending while answered_at is NULL. The transaction that inserts the user message that answers or retires it sets answered_at, and a new question of the chat clears it.';
COMMENT ON COLUMN chat_descendant_questions.root_chat_id IS 'The root of the chat''s family, or the chat itself when it names no root.';
COMMENT ON COLUMN chat_descendant_questions.answered_by IS 'What retired the question: message (a user-visible user message that answers a question that is not secret), secret_answer (a user message that answers this call of a secret question, such as the message of the secret answer route), context_boundary (a clear or a compaction), or migrated (retired before answered_at was stored).';
