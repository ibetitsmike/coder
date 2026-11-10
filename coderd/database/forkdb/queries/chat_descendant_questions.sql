-- name: UpsertChatDescendantQuestion :one
INSERT INTO chat_descendant_questions (chat_id, tool_call_id, questions)
VALUES (@chat_id, @tool_call_id, @questions)
ON CONFLICT (chat_id) DO UPDATE SET
    tool_call_id = EXCLUDED.tool_call_id,
    questions = EXCLUDED.questions,
    created_at = now()
RETURNING *;

-- name: GetPendingChatDescendantQuestion :one
SELECT q.*
FROM chat_descendant_questions q
WHERE q.chat_id = @chat_id::uuid
    AND NOT EXISTS (
        SELECT 1
        FROM chat_messages m
        WHERE m.chat_id = q.chat_id
            AND m.role = 'user'
            AND m.created_at > q.created_at
            AND (
                (NOT m.deleted
                    AND (
                        -- A context boundary, such as a clear, retires the question.
                        m.compressed
                        OR (m.visibility <> 'model'
                            -- Fails closed like forkdb.ChatDescendantQuestion.Secret.
                            AND jsonb_typeof(q.questions) = 'array'
                            AND NOT jsonb_path_exists(q.questions, '$[*] ? (@.secret == true)'))
                    ))
                -- Only the message of the secret answer route, which starts
                -- with coderd's questionAnswerPrefix, answers a secret
                -- question. A queued message that the turn's end promotes
                -- leaves it pending. The message retires the question even
                -- after an edit deletes it, since the consumed answer must not
                -- be asked for again.
                OR (m.visibility <> 'model'
                    AND EXISTS (
                        SELECT 1
                        FROM jsonb_array_elements(CASE WHEN jsonb_typeof(m.content) = 'array' THEN m.content ELSE '[]'::jsonb END) AS part
                        WHERE part->>'type' = 'text'
                            AND starts_with(part->>'text', 'Answer to ask_user_question call ' || q.tool_call_id || ':')
                    ))
            )
    );

-- name: ListPendingChatDescendantQuestionsByRoot :many
-- Lists the unanswered questions of the unarchived descendants of
-- @root_chat_id, oldest first, with the pending rule of
-- GetPendingChatDescendantQuestion.
SELECT q.chat_id, q.tool_call_id, q.questions, q.created_at,
    c.title AS chat_title, c.parent_chat_id
FROM chat_descendant_questions q
JOIN chats c ON c.id = q.chat_id
WHERE c.root_chat_id = @root_chat_id::uuid
    AND c.id <> @root_chat_id::uuid
    AND c.archived = false
    AND NOT EXISTS (
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
    )
ORDER BY q.created_at, q.chat_id;
