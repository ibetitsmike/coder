-- name: InsertChatSecretAnswer :one
INSERT INTO chat_secret_answers (chat_id, tool_call_id, value, value_key_id)
VALUES (@chat_id, @tool_call_id, @value, @value_key_id)
RETURNING created_at;

-- name: GetChatSecretAnswersByChatID :many
SELECT * FROM chat_secret_answers WHERE chat_id = @chat_id ORDER BY created_at;

-- name: DeleteChatSecretAnswer :exec
-- A non-null created_at deletes the answer only if it is still the one
-- created at that time, not a later answer that replaced it.
DELETE FROM chat_secret_answers
WHERE chat_id = @chat_id AND tool_call_id = @tool_call_id
    AND (sqlc.narg('created_at')::timestamptz IS NULL OR created_at = sqlc.narg('created_at'));

-- name: DeleteChatSecretAnswersByChatID :exec
DELETE FROM chat_secret_answers WHERE chat_id = @chat_id;

-- name: DeleteOldChatSecretAnswers :execrows
DELETE FROM chat_secret_answers WHERE created_at < @before_time::timestamptz;
