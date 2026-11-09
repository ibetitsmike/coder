-- name: GetChatSubagentByChatID :one
SELECT * FROM chat_subagents WHERE chat_id = @chat_id::uuid;
