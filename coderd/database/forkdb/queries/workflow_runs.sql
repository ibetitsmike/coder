-- name: InsertWorkflowRun :exec
INSERT INTO workflow_runs (id, chat_id, model_config_id, script, args, status)
VALUES (@id, @chat_id, @model_config_id, @script, sqlc.arg(args)::jsonb, @status);

-- name: GetWorkflowRun :one
SELECT * FROM workflow_runs WHERE id = @id;

-- name: GetWorkflowStepsByRun :many
SELECT * FROM workflow_steps WHERE run_id = @run_id ORDER BY ordinal;

-- name: UpdateWorkflowRunStatus :exec
-- result is the full terminal snapshot of the run, or NULL for interrupted.
-- The param stays uncast so sqlc ties it to the nullable column and applies
-- the NullRawMessage override: a plain json.RawMessage would reach pq as a
-- zero-length string, which Postgres rejects on jsonb.
UPDATE workflow_runs
SET status = @status, error = @error, result = @result
WHERE id = @id;

-- name: MarkOpenWorkflowRunsInterrupted :execrows
-- chatd runs this once at startup: runs owned by a dead process stay open.
-- The parameter is err_text, not error: it becomes a mock parameter name.
UPDATE workflow_runs SET status = 'interrupted', error = @err_text
WHERE status = 'running';

-- name: InsertWorkflowStep :exec
INSERT INTO workflow_steps (id, run_id, spec_key, occurrence, ordinal, title, status, value)
VALUES (@id, @run_id, @spec_key, @occurrence, @ordinal, @title, @status, @value);

-- name: GetWorkflowStep :one
-- The authorizer of UpdateWorkflowStep reads the step to find its run.
SELECT * FROM workflow_steps WHERE id = @id;

-- name: UpdateWorkflowStep :exec
UPDATE workflow_steps
SET status = @status, value = @value, error = @error
WHERE id = @id;
