-- name: GetChatSubtreeIDs :many
-- Returns every descendant of @root_id through parent_chat_id, leaves first.
-- The depth guard stops the walk if a cycle ever appears in the hierarchy.
WITH RECURSIVE subtree(id, depth) AS (
	SELECT chats.id, 1
	FROM chats
	WHERE chats.parent_chat_id = @root_id::uuid
	UNION ALL
	SELECT chats.id, subtree.depth + 1
	FROM chats
	JOIN subtree ON chats.parent_chat_id = subtree.id
	WHERE subtree.depth < 64
)
SELECT subtree.id, subtree.depth::integer AS depth
FROM subtree
ORDER BY subtree.depth DESC, subtree.id;
