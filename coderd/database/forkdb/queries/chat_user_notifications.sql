-- Records a notify_user call that passed the hourly limit of its chat, or a
-- wake-up pause notification sent about it.
-- name: InsertChatUserNotification :exec
INSERT INTO chat_user_notifications (chat_id, created_at)
VALUES (@chat_id, @created_at);

-- Counts the notifications of a chat since a time, for the hourly limit.
-- name: CountChatUserNotificationsSince :one
SELECT COUNT(*)
FROM chat_user_notifications
WHERE chat_id = @chat_id
    AND created_at >= @since::timestamptz;

-- Deletes at most @limit_count rows older than a day, on the database clock.
-- The hourly limit never reads them.
-- name: DeleteOldChatUserNotifications :execrows
DELETE FROM chat_user_notifications
WHERE id = ANY(ARRAY(
    SELECT id
    FROM chat_user_notifications
    WHERE created_at < now() - interval '1 day'
    ORDER BY created_at
    LIMIT @limit_count::int
));
