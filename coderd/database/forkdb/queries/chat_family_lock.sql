-- name: LockChatForNoKeyUpdate :one
-- Locks a chat row FOR NO KEY UPDATE. Unlike FOR UPDATE, the lock does not
-- conflict with the FOR KEY SHARE locks that foreign key checks of child chat
-- rows take, so a child transaction can commit while the lock is held.
SELECT id FROM chats WHERE id = @id::uuid FOR NO KEY UPDATE;
