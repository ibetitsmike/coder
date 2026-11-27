-- name: InsertSandboxOperation :one
-- Writes the intent of one build. The workspace, transition and generation
-- come from the build row, so a caller cannot file an intent under another
-- workspace. A second insert for the same build returns the existing row.
INSERT INTO sandbox_operations (id, workspace_id, workspace_build_id, transition, generation)
SELECT @id::uuid, wb.workspace_id, wb.id, wb.transition, wb.build_number
FROM workspace_builds AS wb
WHERE wb.id = @workspace_build_id::uuid
ON CONFLICT (workspace_build_id) DO UPDATE SET updated_at = sandbox_operations.updated_at
RETURNING *;

-- name: GetSandboxOperation :one
SELECT * FROM sandbox_operations WHERE id = @id;

-- name: SupersedeSandboxOperations :many
-- Marks the unfinished operations of older generations of a workspace as
-- superseded and returns them. The caller learns which builds a crashed or
-- fenced worker left unfinished.
UPDATE sandbox_operations
SET status = 'superseded', updated_at = now()
WHERE workspace_id = @workspace_id
	AND generation < @generation
	AND status IN ('intent', 'claimed')
RETURNING *;

-- name: ClaimSandboxOperation :one
-- Claims an intent of the newest build of the workspace. The workspace
-- ID is bound, so a caller authorized for one workspace cannot claim the
-- operation of another. A stale generation or a second claim returns no row.
UPDATE sandbox_operations AS op
SET status = 'claimed', updated_at = now()
WHERE op.id = @id
	AND op.workspace_id = @workspace_id
	AND op.status = 'intent'
	AND op.generation = (
		SELECT MAX(wb.build_number) FROM workspace_builds AS wb
		WHERE wb.workspace_id = @workspace_id
	)
RETURNING op.*;

-- name: CompleteSandboxOperation :one
-- Finishes a claimed operation as done or failed. It returns no row when the
-- operation is not claimed, belongs to another workspace, or a newer
-- build of the workspace exists, so a fenced worker cannot finish.
UPDATE sandbox_operations AS op
SET status = sqlc.arg(status)::text, updated_at = now()
WHERE op.id = @id
	AND op.workspace_id = @workspace_id
	AND op.status = 'claimed'
	AND sqlc.arg(status)::text IN ('done', 'failed')
	AND op.generation = (
		SELECT MAX(wb.build_number) FROM workspace_builds AS wb
		WHERE wb.workspace_id = @workspace_id
	)
RETURNING op.*;
