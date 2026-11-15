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
-- a resume can end it later. The param stays uncast so sqlc ties it to the
-- nullable column and applies the NullRawMessage override: a plain
-- json.RawMessage would reach pq as a zero-length string, which Postgres
-- rejects on jsonb.
UPDATE workflow_runs
SET status = @status, error = @error, result = @result,
    updated_at = now(),
    finished_at = CASE WHEN @status::text IN ('completed', 'failed', 'stopped') THEN now() ELSE NULL END
WHERE id = @id;

-- name: MarkOpenWorkflowRunsInterrupted :execrows
-- chatd runs this once at startup: runs owned by a dead process stay open.
-- The parameter is err_text, not error: it becomes a mock parameter name.
UPDATE workflow_runs SET status = 'interrupted', error = @err_text, updated_at = now()
WHERE status = 'running';

-- name: InsertWorkflowStep :exec
INSERT INTO workflow_steps (id, run_id, spec_key, occurrence, ordinal, title, status, value, reused)
VALUES (@id, @run_id, @spec_key, @occurrence, @ordinal, @title, @status, @value, @reused);

-- name: GetWorkflowStep :one
-- The authorizer of UpdateWorkflowStep reads the step to find its run.
SELECT * FROM workflow_steps WHERE id = @id;

-- name: UpdateWorkflowStep :exec
-- A reported or failed step stamps finished_at; started_at is set once, at
-- the insert. The param stays uncast so sqlc ties it to the nullable column
-- and applies the NullRawMessage override.
UPDATE workflow_steps
SET status = @status, value = @value, error = @error, reused = @reused,
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

-- name: MarkWorkflowStepsReusedByRun :exec
-- The resume of a terminal run replays every step of the stored snapshot, so
-- the run detail reads the flag from the rows without the snapshot.
UPDATE workflow_steps SET reused = true
WHERE run_id = @run_id AND NOT reused;

-- name: FailOpenWorkflowStepsOfRun :execrows
-- stop_workflow fails the open steps of a run that it stops. err_text, not
-- error: it becomes a mock parameter name.
UPDATE workflow_steps SET status = 'failed', error = @err_text, finished_at = now()
WHERE run_id = @run_id AND status IN ('running', 'reminded');

-- name: SetWorkflowRunLogs :exec
-- The engine writes the log lines of a live run through to the journal, capped
-- to the last workflowLiveLogs lines. lines replaces the retained lines, so
-- the cap holds across restarts and the detail of a mid-run read matches the
-- terminal snapshot.
UPDATE workflow_runs
SET logs = sqlc.arg(lines)::jsonb, updated_at = now()
WHERE id = @id;
