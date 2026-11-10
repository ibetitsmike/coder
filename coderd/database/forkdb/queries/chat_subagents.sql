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
SELECT chat_id, agent_type FROM chat_subagents WHERE chat_id = ANY(@chat_ids::uuid[]);
