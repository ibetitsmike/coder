-- name: InsertUserAgentDefinition :one
INSERT INTO agent_definitions (organization_id, user_id, name, description, content)
VALUES (@organization_id::uuid, @user_id::uuid, @name::text, @description::text, @content::text)
RETURNING *;

-- name: GetUserAgentDefinitionByName :one
SELECT *
FROM agent_definitions
WHERE organization_id = @organization_id::uuid AND user_id = @user_id::uuid AND name = @name::text;

-- name: ListUserAgentDefinitionMetadata :many
SELECT
    id, organization_id, user_id, name, description, enabled, created_at, updated_at
FROM agent_definitions
WHERE organization_id = @organization_id::uuid AND user_id = @user_id::uuid
ORDER BY name ASC;

-- name: UpdateUserAgentDefinitionByName :one
UPDATE agent_definitions
SET
    description = COALESCE(sqlc.narg('description')::text, description),
    content     = COALESCE(sqlc.narg('content')::text, content),
    enabled     = COALESCE(sqlc.narg('enabled')::boolean, enabled),
    updated_at  = now()
WHERE organization_id = @organization_id::uuid AND user_id = @user_id::uuid AND name = @name::text
RETURNING *;

-- name: DeleteUserAgentDefinitionByName :one
DELETE FROM agent_definitions
WHERE organization_id = @organization_id::uuid AND user_id = @user_id::uuid AND name = @name::text
RETURNING *;

-- name: InsertOrganizationAgentDefinition :one
INSERT INTO agent_definitions (organization_id, name, description, content, group_acl, user_acl)
VALUES (@organization_id::uuid, @name::text, @description::text, @content::text, @group_acl, @user_acl)
RETURNING *;

-- name: GetOrganizationAgentDefinitionByName :one
SELECT *
FROM agent_definitions
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND project_id IS NULL AND name = @name::text;

-- name: ListOrganizationAgentDefinitionMetadata :many
SELECT
    id, organization_id, name, description, enabled, group_acl, user_acl, created_at, updated_at
FROM agent_definitions
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND project_id IS NULL
ORDER BY name ASC;

-- name: UpdateOrganizationAgentDefinitionByName :one
UPDATE agent_definitions
SET
    description = COALESCE(sqlc.narg('description')::text, description),
    content     = COALESCE(sqlc.narg('content')::text, content),
    enabled     = COALESCE(sqlc.narg('enabled')::boolean, enabled),
    updated_at  = now()
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND project_id IS NULL AND name = @name::text
RETURNING *;

-- name: DeleteOrganizationAgentDefinitionByName :one
DELETE FROM agent_definitions
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND project_id IS NULL AND name = @name::text
RETURNING *;
