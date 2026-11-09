-- name: GetChatOrganizationSubagentLimits :one
SELECT * FROM chat_organization_subagent_limits WHERE organization_id = @organization_id::uuid;

-- name: UpsertChatOrganizationSubagentLimits :one
INSERT INTO chat_organization_subagent_limits (organization_id, max_depth, max_concurrent, max_concurrent_per_family, max_live_per_family)
VALUES (@organization_id, sqlc.narg('max_depth')::integer, sqlc.narg('max_concurrent')::integer, sqlc.narg('max_concurrent_per_family')::integer, sqlc.narg('max_live_per_family')::integer)
ON CONFLICT (organization_id) DO UPDATE SET
    max_depth = EXCLUDED.max_depth,
    max_concurrent = EXCLUDED.max_concurrent,
    max_concurrent_per_family = EXCLUDED.max_concurrent_per_family,
    max_live_per_family = EXCLUDED.max_live_per_family,
    updated_at = now()
RETURNING *;
