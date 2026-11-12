-- name: InsertWorkflowRun :exec
INSERT INTO workflow_runs (id, chat_id, model_config_id, script, args, status)
VALUES (@id, @chat_id, @model_config_id, @script, sqlc.arg(args)::jsonb, @status);

-- name: GetWorkflowRun :one
SELECT * FROM workflow_runs WHERE id = @id;

-- name: GetWorkflowStepsByRun :many
SELECT * FROM workflow_steps WHERE run_id = @run_id ORDER BY ordinal;

-- name: UpdateWorkflowRunStatus :exec
-- result is the full terminal snapshot of the run, or NULL for interrupted.
-- A terminal status stamps finished_at; interrupted keeps the run open, so
-- a resume can end it later.
UPDATE workflow_runs
SET status = @status, error = @error, result = sqlc.arg(result)::jsonb,
    updated_at = now(),
    finished_at = CASE WHEN @status::text IN ('completed', 'failed', 'stopped') THEN now() ELSE NULL END
WHERE id = @id;

-- name: MarkOpenWorkflowRunsInterrupted :execrows
-- chatd runs this once at startup: runs owned by a dead process stay open.
-- The parameter is err_text, not error: it becomes a mock parameter name.
UPDATE workflow_runs SET status = 'interrupted', error = @err_text, updated_at = now()
WHERE status = 'running';

-- name: InsertWorkflowStep :exec
INSERT INTO workflow_steps (id, run_id, spec_key, occurrence, ordinal, title, status, value)
VALUES (@id, @run_id, @spec_key, @occurrence, @ordinal, @title, @status, sqlc.arg(value)::jsonb);

-- name: GetWorkflowStep :one
-- The authorizer of UpdateWorkflowStep reads the step to find its run.
SELECT * FROM workflow_steps WHERE id = @id;

-- name: UpdateWorkflowStep :exec
-- A reported or failed step stamps finished_at; started_at is set once, at
-- the insert.
UPDATE workflow_steps
SET status = @status, value = sqlc.arg(value)::jsonb, error = @error,
    finished_at = CASE WHEN @status::text IN ('reported', 'failed') THEN now() ELSE NULL END
WHERE id = @id;

-- name: GetWorkflowRunsByChat :many
-- The run list of a chat, newest first, one keyset page. after_created_at
-- and after_id are the created_at and the ID of the last run of the page the
-- client already has; both NULL reads the first page.
SELECT * FROM workflow_runs
WHERE chat_id = @chat_id
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::int;

-- name: GetWorkflowRunStepCountsByChat :many
-- One row per run and step status of a chat, for the step_counts of the run
-- list. done means reported; running means running or reminded.
SELECT s.run_id, s.status, count(*)::int AS step_count
FROM workflow_steps s
JOIN workflow_runs r ON r.id = s.run_id
WHERE r.chat_id = @chat_id
GROUP BY s.run_id, s.status;

-- name: FailOpenWorkflowStepsOfRun :execrows
-- stop_workflow fails the open steps of a run that it stops. err_text, not
-- error: it becomes a mock parameter name.
UPDATE workflow_steps SET status = 'failed', error = @err_text, finished_at = now()
WHERE run_id = @run_id AND status IN ('running', 'reminded');
