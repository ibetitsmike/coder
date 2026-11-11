-- name: InsertPluginPack :one
INSERT INTO plugin_packs (
    id,
    organization_id,
    name,
    source_url,
    source_path,
    ref,
    commit_sha,
    created_by,
    created_at,
    updated_at
) VALUES (
    @id,
    @organization_id,
    @name,
    @source_url,
    @source_path,
    @ref,
    @commit_sha,
    @created_by,
    @created_at,
    @updated_at
)
RETURNING *;

-- name: GetPluginPackByID :one
SELECT * FROM plugin_packs WHERE id = @id;

-- name: GetPluginPacksByOrganizationID :many
SELECT * FROM plugin_packs WHERE organization_id = @organization_id ORDER BY name;

-- name: UpdatePluginPackCommit :one
UPDATE plugin_packs
SET
    ref = @ref,
    commit_sha = @commit_sha,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: DeletePluginPackByID :exec
DELETE FROM plugin_packs WHERE id = @id;

-- name: InsertPluginPackResources :exec
INSERT INTO plugin_pack_resources (pack_id, path, content, content_hash)
SELECT
    @pack_id::uuid,
    unnest(@paths::text[]),
    unnest(@contents::bytea[]),
    unnest(@content_hashes::text[]);

-- name: DeletePluginPackResources :exec
DELETE FROM plugin_pack_resources WHERE pack_id = @pack_id;

-- name: GetPluginPackResources :many
SELECT * FROM plugin_pack_resources WHERE pack_id = @pack_id ORDER BY path;

-- name: GetPluginPackSources :one
SELECT * FROM plugin_pack_sources WHERE organization_id = @organization_id;

-- name: UpsertPluginPackSources :one
INSERT INTO plugin_pack_sources (organization_id, hosts, updated_at)
VALUES (@organization_id, @hosts::text[], @updated_at)
ON CONFLICT (organization_id) DO UPDATE
SET
    hosts = EXCLUDED.hosts,
    updated_at = EXCLUDED.updated_at
RETURNING *;
