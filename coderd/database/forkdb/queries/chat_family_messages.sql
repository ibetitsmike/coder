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

-- name: ListChatFamilyMembers :many
-- Lists the unarchived chats of the family of @root_id, the root first.
SELECT chats.id, chats.parent_chat_id, chats.title, chats.status
FROM chats
WHERE (chats.id = @root_id::uuid OR COALESCE(chats.root_chat_id, chats.parent_chat_id) = @root_id::uuid)
	AND chats.archived = false
ORDER BY (chats.id = @root_id::uuid) DESC, chats.created_at ASC, chats.id ASC
LIMIT @limit_count::int;
