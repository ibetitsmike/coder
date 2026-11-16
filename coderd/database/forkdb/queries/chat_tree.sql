-- name: GetDescendantChatsByRootIDs :many
-- Returns every descendant of the given root chats in one statement, with
-- the row shape of upstream's GetChildChatsByParentIDs, optionally filtered
-- by archive state (NULL = all, true/false = match). A descendant whose
-- parent is filtered out is still returned; the caller drops it, so that a
-- hidden chat hides its subtree.
SELECT
    sqlc.embed(chats_expanded),
    EXISTS (
        SELECT 1 FROM chat_messages cm
        WHERE cm.chat_id = chats_expanded.id
            AND cm.role = 'assistant'
            AND cm.deleted = false
            AND cm.id > COALESCE(chats_expanded.last_read_message_id, 0)
    ) AS has_unread
FROM
    chats_expanded
WHERE
    -- A direct child created without root_chat_id still hangs off its
    -- parent.
    (
        chats_expanded.root_chat_id = ANY(@root_ids :: uuid[])
        OR chats_expanded.parent_chat_id = ANY(@root_ids :: uuid[])
    )
    AND chats_expanded.parent_chat_id IS NOT NULL
    AND CASE
        WHEN sqlc.narg('archived') :: boolean IS NULL THEN true
        ELSE chats_expanded.archived = sqlc.narg('archived') :: boolean
    END
ORDER BY
    chats_expanded.created_at DESC,
    chats_expanded.id DESC;
