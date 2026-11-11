-- name: InsertSandboxOperation :one
INSERT INTO sandbox_operations (id, workspace_id, workspace_build_id, operation, generation, status)
VALUES (@id, @workspace_id, @workspace_build_id, @operation, @generation, 'intent')
RETURNING *;

-- name: GetSandboxOperation :one
SELECT * FROM sandbox_operations WHERE id = @id;

-- name: GetSandboxOperationsByBuildID :many
SELECT * FROM sandbox_operations
WHERE workspace_build_id = @workspace_build_id
ORDER BY created_at;

-- name: ClaimSandboxOperation :one
-- Claims the operation when its generation is the newest one of the
-- workspace and it is still an intent. A stale generation cannot claim.
UPDATE sandbox_operations AS op
SET status = 'claimed', updated_at = now()
WHERE op.id = @id
	AND op.generation = (
		SELECT MAX(latest.generation) FROM sandbox_operations AS latest
		WHERE latest.workspace_id = @workspace_id
	)
	AND op.status = 'intent'
RETURNING op.*;

-- name: CompleteSandboxOperation :one
-- Marks a claimed operation done or failed. Completing twice is rejected.
UPDATE sandbox_operations AS op
SET status = @status, updated_at = now()
WHERE op.id = @id AND op.status = 'claimed'
RETURNING op.*;