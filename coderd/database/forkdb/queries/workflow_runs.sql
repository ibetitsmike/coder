-- name: InsertWorkflowRun :exec
-- phases holds the declared phases of the script plus every undeclared name
-- that phase() reached, in display order. Empty when the script declares no
-- phases. The ::text[] cast makes sqlc emit []string wrapped in pq.Array; a
-- bare COALESCE(@phases, ...) leaves it interface{}. COALESCE keeps a nil
-- slice out of the NOT NULL column: pq encodes a nil []string as NULL.
INSERT INTO workflow_runs (id, chat_id, model_config_id, script, args, status, phases, current_phase)
VALUES (@id, @chat_id, @model_config_id, @script, sqlc.arg(args)::jsonb, @status, COALESCE(@phases::text[], '{}'::text[]), @current_phase);

-- name: GetWorkflowRun :one
SELECT * FROM workflow_runs WHERE id = @id;

-- name: GetWorkflowStepsByRun :many
SELECT * FROM workflow_steps WHERE run_id = @run_id ORDER BY ordinal;

-- name: UpdateWorkflowRunPhases :exec
-- phase() journals the phase state of a live run as it changes: the display
-- list and the current name. The write does not touch status, error or
-- result, which updateRunStatus owns, so the two best-effort writers never
-- overwrite each other's columns.
UPDATE workflow_runs
SET phases = COALESCE(@phases::text[], '{}'::text[]), current_phase = @current_phase, updated_at = now()
WHERE id = @id;

-- name: UpdateWorkflowRunStatus :exec
-- result is the full terminal snapshot of the run, or NULL for interrupted.
-- A terminal status stamps finished_at; interrupted keeps the run open, so
-- a resume can end it later. The param stays uncast so sqlc ties it to the
-- nullable column and applies the NullRawMessage override: a plain
-- json.RawMessage would reach pq as a zero-length string, which Postgres
-- rejects on jsonb.
UPDATE workflow_runs
SET status = @status, error = @error, result = @result,
    phases = COALESCE(@phases::text[], '{}'::text[]), current_phase = @current_phase,
    updated_at = now(),
    finished_at = CASE WHEN @status::text IN ('completed', 'failed', 'stopped') THEN now() ELSE NULL END
WHERE id = @id;

-- name: MarkOpenWorkflowRunsInterrupted :execrows
-- chatd runs this once at startup: runs owned by a dead process stay open.
-- The parameter is err_text, not error: it becomes a mock parameter name.
UPDATE workflow_runs SET status = 'interrupted', error = @err_text, updated_at = now()
WHERE status = 'running';

-- name: InsertWorkflowStep :exec
-- phase is the name that was current when the step started, or '' when the
-- script called no phase() before it.
INSERT INTO workflow_steps (id, run_id, spec_key, occurrence, ordinal, title, status, value, reused, phase)
VALUES (@id, @run_id, @spec_key, @occurrence, @ordinal, @title, @status, @value, @reused, @phase);

-- name: GetWorkflowStep :one
-- The authorizer of UpdateWorkflowStep reads the step to find its run.
SELECT * FROM workflow_steps WHERE id = @id;

-- name: UpdateWorkflowStep :exec
-- A reported or failed step stamps finished_at; started_at is set once, at
-- the insert. The param stays uncast so sqlc ties it to the nullable column
-- and applies the NullRawMessage override. reused only sets the flag: a
-- report from a step that a resume reused must not clear it.
UPDATE workflow_steps
SET status = @status, value = @value, error = @error,
    reused = reused OR @reused,
    finished_at = CASE WHEN @status::text IN ('reported', 'failed') THEN now() ELSE NULL END
WHERE id = @id;

-- name: GetWorkflowRunsByChat :many
-- The run list of a chat, newest first, one keyset page. after_created_at
-- and after_id are the created_at and the ID of the last run of the page the
-- client already has; both NULL reads the first page. The list needs no logs,
-- args or result, so it does not read those columns: logs alone can hold the
-- full retained tail of every run.
SELECT id, chat_id, model_config_id, script, status, error, created_at, updated_at, finished_at, phases, current_phase
FROM workflow_runs
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

-- name: MarkWorkflowStepReused :exec
-- The replay of a finished step flips only the reuse flag: its status, value
-- and finished_at stay as the journal holds them.
UPDATE workflow_steps SET reused = true WHERE id = @id;

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
-- to the last maxWorkflowLogs lines. lines replaces the retained lines when
-- it is at least as new as the stored sequence, so a stalled write of an
-- older snapshot cannot replace newer lines. The caller keeps the sequence in
-- the lines payload under the fork_seq key.
UPDATE workflow_runs
SET logs = sqlc.arg(lines)::jsonb, updated_at = now()
WHERE id = @id
	AND (
		logs IS NULL
		OR logs = 'null'::jsonb
		OR COALESCE(logs->'fork_seq', '0'::jsonb)::bigint <= sqlc.arg(fork_seq)::bigint
	);
