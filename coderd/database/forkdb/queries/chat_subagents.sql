-- name: GetChatSubagentByChatID :one
SELECT * FROM chat_subagents WHERE chat_id = @chat_id::uuid;

-- name: InsertChatSubagent :one
INSERT INTO chat_subagents (chat_id, agent_type, depth, isolation, worktree_path, branch, workspace_id)
VALUES (@chat_id, @agent_type, @depth, @isolation, @worktree_path, @branch, @workspace_id)
RETURNING *;
