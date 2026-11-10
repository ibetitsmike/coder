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
            -- A context boundary, such as a clear, retires the question.
            AND (m.visibility <> 'model' OR m.compressed)
            AND NOT m.deleted
            AND m.created_at > q.created_at
    );

-- name: ListPendingChatDescendantQuestionsByRoot :many
-- Lists the unanswered questions of the unarchived descendants of
-- @root_chat_id, oldest first. A question is pending until its chat gets a
-- non-deleted user message newer than the question, like
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
            AND m.visibility <> 'model'
            AND NOT m.deleted
            AND m.created_at > q.created_at
    )
ORDER BY q.created_at, q.chat_id;
