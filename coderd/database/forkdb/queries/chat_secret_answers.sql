-- name: InsertChatSecretAnswer :exec
INSERT INTO chat_secret_answers (chat_id, tool_call_id, value, value_key_id)
VALUES (@chat_id, @tool_call_id, @value, @value_key_id);

-- name: GetChatSecretAnswersByChatID :many
SELECT * FROM chat_secret_answers WHERE chat_id = @chat_id ORDER BY created_at;

-- name: DeleteChatSecretAnswer :exec
DELETE FROM chat_secret_answers WHERE chat_id = @chat_id AND tool_call_id = @tool_call_id;

-- name: DeleteChatSecretAnswersByChatID :exec
DELETE FROM chat_secret_answers WHERE chat_id = @chat_id;

-- name: DeleteOldChatSecretAnswers :execrows
DELETE FROM chat_secret_answers WHERE created_at < @before_time::timestamptz;
