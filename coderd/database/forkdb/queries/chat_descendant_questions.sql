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
            AND m.visibility <> 'model'
            AND NOT m.deleted
            AND m.created_at > q.created_at
    );
