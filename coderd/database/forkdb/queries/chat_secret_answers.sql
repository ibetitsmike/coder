-- name: InsertChatSecretAnswer :exec
INSERT INTO chat_secret_answers (chat_id, tool_call_id, value, value_key_id)
VALUES (@chat_id, @tool_call_id, @value, @value_key_id);

-- name: GetChatSecretAnswersByChatID :many
-- Answers expire after 24 hours; dbpurge deletes them with the same TTL.
SELECT * FROM chat_secret_answers
WHERE chat_id = @chat_id AND created_at > now() - interval '24 hours'
ORDER BY created_at;

-- name: DeleteChatSecretAnswer :exec
DELETE FROM chat_secret_answers WHERE chat_id = @chat_id AND tool_call_id = @tool_call_id;

-- name: DeleteOldChatSecretAnswers :execrows
-- Deletes a batch of answers past the 24 hour TTL of
-- GetChatSecretAnswersByChatID, on the same clock.
WITH expired AS (
    SELECT chat_id, tool_call_id
    FROM chat_secret_answers
    WHERE created_at <= now() - interval '24 hours'
    ORDER BY created_at
    LIMIT @limit_count::int
)
DELETE FROM chat_secret_answers
USING expired
WHERE chat_secret_answers.chat_id = expired.chat_id
    AND chat_secret_answers.tool_call_id = expired.tool_call_id;
