-- name: InsertUserSavedWorkflow :one
INSERT INTO saved_workflows (organization_id, user_id, created_by, name, description, script, args_schema, phases)
VALUES (@organization_id::uuid, @user_id::uuid, @user_id::uuid, @name::text, @description::text, @script::text, @args_schema, COALESCE(@phases::text[], '{}'))
RETURNING *;

-- name: GetUserSavedWorkflowByName :one
SELECT *
FROM saved_workflows
WHERE organization_id = @organization_id::uuid AND user_id = @user_id::uuid AND name = @name::text;

-- name: ListUserSavedWorkflowMetadata :many
SELECT
    id, organization_id, user_id, created_by, name, description, args_schema, phases, created_at, updated_at
FROM saved_workflows
WHERE organization_id = @organization_id::uuid AND user_id = @user_id::uuid
ORDER BY name ASC;

-- name: UpdateUserSavedWorkflowByName :one
UPDATE saved_workflows
SET
    description = COALESCE(sqlc.narg('description')::text, description),
    script      = COALESCE(sqlc.narg('script')::text, script),
    args_schema = CASE WHEN @set_args_schema::boolean THEN @args_schema ELSE args_schema END,
    phases      = COALESCE(sqlc.narg('phases')::text[], phases),
    updated_at  = now()
WHERE organization_id = @organization_id::uuid AND user_id = @user_id::uuid AND name = @name::text
RETURNING *;

-- name: DeleteUserSavedWorkflowByName :one
DELETE FROM saved_workflows
WHERE organization_id = @organization_id::uuid AND user_id = @user_id::uuid AND name = @name::text
RETURNING *;

-- name: InsertOrganizationSavedWorkflow :one
INSERT INTO saved_workflows (organization_id, created_by, name, description, script, args_schema, phases, group_acl, user_acl)
VALUES (@organization_id::uuid, @created_by::uuid, @name::text, @description::text, @script::text, @args_schema, COALESCE(@phases::text[], '{}'), @group_acl, @user_acl)
RETURNING *;

-- name: GetOrganizationSavedWorkflowByName :one
SELECT *
FROM saved_workflows
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND name = @name::text;

-- name: GetOrganizationSavedWorkflowByNameForUpdate :one
SELECT *
FROM saved_workflows
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND name = @name::text
FOR UPDATE;

-- name: ListOrganizationSavedWorkflowMetadata :many
SELECT
    id, organization_id, created_by, name, description, args_schema, phases, group_acl, user_acl, created_at, updated_at
FROM saved_workflows
WHERE organization_id = @organization_id::uuid AND user_id IS NULL
ORDER BY name ASC;

-- name: UpdateOrganizationSavedWorkflowByName :one
UPDATE saved_workflows
SET
    description = COALESCE(sqlc.narg('description')::text, description),
    script      = COALESCE(sqlc.narg('script')::text, script),
    args_schema = CASE WHEN @set_args_schema::boolean THEN @args_schema ELSE args_schema END,
    phases      = COALESCE(sqlc.narg('phases')::text[], phases),
    updated_at  = now()
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND name = @name::text
RETURNING *;

-- name: UpdateOrganizationSavedWorkflowACLByName :one
UPDATE saved_workflows
SET
    group_acl  = @group_acl,
    user_acl   = @user_acl,
    updated_at = now()
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND name = @name::text
RETURNING *;

-- name: DeleteOrganizationSavedWorkflowByName :one
DELETE FROM saved_workflows
WHERE organization_id = @organization_id::uuid AND user_id IS NULL AND name = @name::text
RETURNING *;
