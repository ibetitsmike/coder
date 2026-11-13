-- name: InsertChatFamilyMessage :exec
INSERT INTO chat_family_messages (id, sender_chat_id, recipient_chat_id, created_at)
VALUES (@id::uuid, @sender_chat_id::uuid, @recipient_chat_id::uuid, @created_at::timestamptz);

-- name: CountChatFamilyMessagesSince :one
SELECT count(*) FROM chat_family_messages
WHERE sender_chat_id = @sender_chat_id::uuid AND created_at > @since::timestamptz;

-- name: GetChatFamilyMessageRecipient :one
SELECT recipient_chat_id FROM chat_family_messages
WHERE id = @id::uuid AND sender_chat_id = @sender_chat_id::uuid;

-- name: DeleteChatFamilyMessagesBefore :exec
DELETE FROM chat_family_messages
WHERE sender_chat_id = @sender_chat_id::uuid AND created_at <= @before::timestamptz;
