-- name: GetDescendantChatsByRootIDs :many
-- Returns the descendants of the given chats up to max_depth levels below
-- them (at most 64) in one statement, with the row
-- shape of upstream's GetChildChatsByParentIDs, optionally filtered by
-- archive state (NULL = all, true/false = match). It follows parent_chat_id
-- level by level as a walk with upstream's query does, so it needs no
-- root_chat_id: a chat whose family root was deleted, and so lost its
-- root_chat_id, still reaches its whole subtree. The walk continues only
-- through chats that match the archive filter. The 64-level cap also stops
-- it if a cycle ever appears in the hierarchy.
WITH RECURSIVE descendants(id, depth) AS (
    SELECT chats.id, 1
    FROM chats
    WHERE chats.parent_chat_id = ANY(@root_ids :: uuid[])
        AND CASE
            WHEN sqlc.narg('archived') :: boolean IS NULL THEN true
            ELSE chats.archived = sqlc.narg('archived') :: boolean
        END
    UNION ALL
    SELECT chats.id, descendants.depth + 1
    FROM chats
    JOIN descendants ON chats.parent_chat_id = descendants.id
    WHERE descendants.depth < LEAST(@max_depth :: integer, 64)
        AND CASE
            WHEN sqlc.narg('archived') :: boolean IS NULL THEN true
            ELSE chats.archived = sqlc.narg('archived') :: boolean
        END
)
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
JOIN descendants ON descendants.id = chats_expanded.id
ORDER BY
    chats_expanded.created_at DESC,
    chats_expanded.id DESC;
