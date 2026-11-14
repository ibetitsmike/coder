-- name: InsertChatSubagentMessage :exec
INSERT INTO chat_subagent_messages (id, organization_id, sender_chat_id, recipient_chat_id, created_at)
VALUES (@id::uuid, @organization_id::uuid, @sender_chat_id::uuid, @recipient_chat_id::uuid, @created_at::timestamptz);

-- name: GetChatSubagentMessageRecipient :one
SELECT recipient_chat_id FROM chat_subagent_messages
WHERE id = @id::uuid AND organization_id = @organization_id::uuid AND sender_chat_id = @sender_chat_id::uuid;

-- name: DeleteChatSubagentMessagesBefore :exec
DELETE FROM chat_subagent_messages
WHERE organization_id = @organization_id::uuid AND sender_chat_id = @sender_chat_id::uuid AND created_at <= @before::timestamptz;
