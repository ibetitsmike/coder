-- name: GetChatSubtreeIDs :many
-- Returns every descendant of @root_id through parent_chat_id, leaves first,
-- with its status, so that a cascade needs no read per descendant.
-- The depth guard stops the walk if a cycle ever appears in the hierarchy.
WITH RECURSIVE subtree(id, status, depth) AS (
	SELECT chats.id, chats.status, 1
	FROM chats
	WHERE chats.parent_chat_id = @root_id::uuid
	UNION ALL
	SELECT chats.id, chats.status, subtree.depth + 1
	FROM chats
	JOIN subtree ON chats.parent_chat_id = subtree.id
	WHERE subtree.depth < 64
)
SELECT subtree.id, subtree.status, subtree.depth::integer AS depth
FROM subtree
ORDER BY subtree.depth DESC, subtree.id;
