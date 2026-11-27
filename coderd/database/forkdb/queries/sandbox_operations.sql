-- name: InsertSandboxOperation :one
-- Writes the intent of one build, derived from the build and workspace rows.
-- A second insert for the same build returns the existing row.
INSERT INTO sandbox_operations (id, organization_id, workspace_id, workspace_build_id, transition, generation)
SELECT @id::uuid, w.organization_id, wb.workspace_id, wb.id, wb.transition, wb.build_number
FROM workspace_builds AS wb
JOIN workspaces AS w ON w.id = wb.workspace_id
WHERE wb.id = @workspace_build_id::uuid
ON CONFLICT (workspace_build_id) DO UPDATE SET updated_at = sandbox_operations.updated_at
RETURNING *;

-- name: GetSandboxOperation :one
SELECT * FROM sandbox_operations WHERE id = @id;

-- name: SupersedeSandboxOperations :many
-- Supersedes the unfinished operations of older generations and returns them.
-- They stay in the result until a newer generation completes as done.
UPDATE sandbox_operations
SET status = 'superseded', updated_at = now()
WHERE workspace_id = @workspace_id
	AND generation < @generation
	AND status IN ('intent', 'claimed', 'superseded')
RETURNING *;

-- name: ClaimSandboxOperation :one
-- Claims an intent of the newest build of the bound workspace. A stale
-- generation, another workspace or a second claim returns no row.
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
-- Finishes a claimed operation of the newest build as done or failed, else
-- returns no row. Done marks older superseded operations reconciled.
WITH reconciled AS (
	UPDATE sandbox_operations AS old
	SET status = 'reconciled', updated_at = now()
	WHERE sqlc.arg(status)::text = 'done'
		AND old.workspace_id = @workspace_id
		AND old.status = 'superseded'
		AND old.generation < (
			SELECT cur.generation FROM sandbox_operations AS cur
			WHERE cur.id = @id AND cur.workspace_id = @workspace_id AND cur.status = 'claimed'
				AND cur.generation = (
					SELECT MAX(wb.build_number) FROM workspace_builds AS wb
					WHERE wb.workspace_id = @workspace_id
				)
		)
)
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
