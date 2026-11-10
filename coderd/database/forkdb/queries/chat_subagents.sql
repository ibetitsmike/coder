-- name: GetChatSubagentByChatID :one
SELECT * FROM chat_subagents WHERE chat_id = @chat_id::uuid;

-- name: InsertChatSubagent :one
INSERT INTO chat_subagents (chat_id, agent_type, depth, isolation, worktree_path, branch, workspace_id, tool_policy)
VALUES (@chat_id, @agent_type, @depth, @isolation, @worktree_path, @branch, @workspace_id, @tool_policy)
RETURNING *;

-- name: GetChatSubagentsByParentChatID :many
SELECT chat_subagents.*
FROM chat_subagents
JOIN chats ON chats.id = chat_subagents.chat_id
WHERE chats.parent_chat_id = @parent_chat_id::uuid;

-- name: GetChatSubagentTypesByChatIDs :many
-- Returns the agent type of each child chat in @chat_ids. A child spawned
-- without a chat_subagents row gets its type from its mode, as
-- subagentTypeFromChat in coderd/x/chatd derives it.
SELECT
	chats.id AS chat_id,
	COALESCE(chat_subagents.agent_type, CASE chats.mode
		WHEN 'explore' THEN 'explore'
		WHEN 'computer_use' THEN 'computer_use'
		ELSE 'general'
	END)::text AS agent_type
FROM chats
LEFT JOIN chat_subagents ON chat_subagents.chat_id = chats.id
WHERE chats.id = ANY(@chat_ids::uuid[])
	AND chats.parent_chat_id IS NOT NULL;
