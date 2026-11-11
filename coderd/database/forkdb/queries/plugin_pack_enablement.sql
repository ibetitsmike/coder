-- name: InsertPluginPackProject :one
-- Enables a pack of the project's organization for the project. It returns
-- no row for a pack that does not exist or is in another organization.
INSERT INTO plugin_pack_projects (organization_id, project_id, pack_id)
SELECT chat_projects.organization_id, chat_projects.id, plugin_packs.id
FROM chat_projects
JOIN plugin_packs ON plugin_packs.organization_id = chat_projects.organization_id
WHERE chat_projects.id = @project_id AND plugin_packs.id = @pack_id
ON CONFLICT (project_id, pack_id) DO UPDATE SET created_at = plugin_pack_projects.created_at
RETURNING *;

-- name: DeletePluginPackProject :exec
DELETE FROM plugin_pack_projects WHERE project_id = @project_id AND pack_id = @pack_id;

-- name: GetPluginPacksByProjectID :many
SELECT plugin_packs.id, plugin_packs.name, plugin_packs.commit_sha
FROM plugin_pack_projects
JOIN plugin_packs ON plugin_packs.id = plugin_pack_projects.pack_id
WHERE plugin_pack_projects.project_id = @project_id
ORDER BY plugin_packs.name;

-- name: InsertChatPluginPack :one
-- Enables a pack of the chat's organization for the chat. It returns no row
-- for a pack that does not exist or is in another organization.
INSERT INTO chat_plugin_packs (organization_id, chat_id, pack_id)
SELECT chats.organization_id, chats.id, plugin_packs.id
FROM chats
JOIN plugin_packs ON plugin_packs.organization_id = chats.organization_id
WHERE chats.id = @chat_id AND plugin_packs.id = @pack_id
ON CONFLICT (chat_id, pack_id) DO UPDATE SET created_at = chat_plugin_packs.created_at
RETURNING *;

-- name: DeleteChatPluginPack :exec
DELETE FROM chat_plugin_packs WHERE chat_id = @chat_id AND pack_id = @pack_id;

-- name: GetPluginPacksByChatID :many
SELECT plugin_packs.id, plugin_packs.name, plugin_packs.commit_sha
FROM chat_plugin_packs
JOIN plugin_packs ON plugin_packs.id = chat_plugin_packs.pack_id
WHERE chat_plugin_packs.chat_id = @chat_id
ORDER BY plugin_packs.name;
