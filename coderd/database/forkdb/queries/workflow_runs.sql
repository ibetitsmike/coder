-- name: InsertWorkflowRun :exec
-- phases holds the declared phases of the script plus every undeclared name
-- that phase() reached, in display order. Empty when the script declares no
-- phases. The ::text[] cast makes sqlc emit []string wrapped in pq.Array; a
-- bare COALESCE(@phases, ...) leaves it interface{}. COALESCE keeps a nil
-- slice out of the NOT NULL column: pq encodes a nil []string as NULL.
-- owner_id and heartbeat_at name the chatd process that runs the script.
INSERT INTO workflow_runs (id, chat_id, model_config_id, script, args, status, phases, current_phase, owner_id, heartbeat_at)
VALUES (@id, @chat_id, @model_config_id, @script, sqlc.arg(args)::jsonb, @status, COALESCE(@phases::text[], '{}'::text[]), @current_phase,
    sqlc.narg('owner_id')::uuid, sqlc.narg('heartbeat_at')::timestamptz);

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
WHERE id = @id
    AND (sqlc.narg('owner_id')::uuid IS NULL OR (owner_id = sqlc.narg('owner_id')::uuid AND status = 'running'));

-- name: UpdateWorkflowRunStatus :execrows
-- result is the full terminal snapshot of the run, or NULL for interrupted.
-- A terminal status stamps finished_at; interrupted keeps the run open, so
-- a resume can end it later. The param stays uncast so sqlc ties it to the
-- nullable column and applies the NullRawMessage override: a plain
-- json.RawMessage would reach pq as a zero-length string, which Postgres
-- rejects on jsonb. owner_id fences the write of a live run: only the
-- process that owns a running run writes it, so a process that lost the run
-- to a sweep or a claim cannot overwrite the state of the new owner. A NULL
-- owner_id skips the fence. The same fence guards every engine write below.
UPDATE workflow_runs
SET status = @status, error = @error, result = @result,
    phases = COALESCE(@phases::text[], '{}'::text[]), current_phase = @current_phase,
    updated_at = now(),
    finished_at = CASE WHEN @status::text IN ('completed', 'failed', 'stopped') THEN now() ELSE NULL END
WHERE id = @id
    AND (sqlc.narg('owner_id')::uuid IS NULL OR (owner_id = sqlc.narg('owner_id')::uuid AND status = 'running'));

-- name: MarkStaleWorkflowRunsInterrupted :execrows
-- chatd runs this at startup and on every heartbeat tick: a running run
-- whose owner stopped its heartbeat belongs to a dead process. A NULL
-- heartbeat is a row written before owners existed. The parameter is
-- err_text, not error: it becomes a mock parameter name.
UPDATE workflow_runs SET status = 'interrupted', error = @err_text, updated_at = now()
WHERE status = 'running'
    AND (heartbeat_at IS NULL OR heartbeat_at < @stale_before::timestamptz);

-- name: HeartbeatWorkflowRuns :many
-- The owner of the runs ids says it still executes them. It returns the IDs
-- that it still owns: a run that is missing was swept or claimed by another
-- process. The heartbeat leaves updated_at alone, which tells the UI that a
-- run changed.
UPDATE workflow_runs SET heartbeat_at = @now::timestamptz
WHERE id = ANY(@ids::uuid[])
    AND owner_id = @owner_id
    AND status = 'running'
RETURNING id;

-- name: ClaimWorkflowRun :one
-- A resume takes over a run: an interrupted run, a running run whose owner
-- stopped its heartbeat, or a run this process already owns. A running run
-- of a live owner returns no row, so two resumes of one run cannot both
-- execute it.
UPDATE workflow_runs
SET owner_id = @owner_id, heartbeat_at = @now::timestamptz, status = 'running', error = '', updated_at = now()
WHERE id = @id
    AND (
        status = 'interrupted'
        OR (status = 'running' AND (
            owner_id = @owner_id
            OR heartbeat_at IS NULL
            OR heartbeat_at < @stale_before::timestamptz
        ))
    )
RETURNING *;

-- name: ReleaseWorkflowRuns :execrows
-- chatd runs this when it closes: the runs it still owns stay open as
-- interrupted at once, so the next process can resume them without waiting
-- for the stale window. err_text, not error: it becomes a mock parameter
-- name.
UPDATE workflow_runs SET status = 'interrupted', error = @err_text, updated_at = now()
WHERE owner_id = @owner_id AND status = 'running';

-- name: InsertWorkflowStep :execrows
-- phase is the name that was current when the step started, or '' when the
-- script called no phase() before it. owner_id fences the insert like
-- UpdateWorkflowRunStatus: a run this process does not own inserts nothing.
INSERT INTO workflow_steps (id, run_id, spec_key, occurrence, ordinal, title, status, value, reused, phase)
SELECT @id::uuid, @run_id::uuid, @spec_key::text, @occurrence::int, @ordinal::int, @title::text, @status::text,
    sqlc.narg('value')::jsonb, @reused::bool, @phase::text
WHERE sqlc.narg('owner_id')::uuid IS NULL
    OR EXISTS (
        SELECT 1 FROM workflow_runs
        WHERE workflow_runs.id = @run_id::uuid
            AND workflow_runs.owner_id = sqlc.narg('owner_id')::uuid
            AND workflow_runs.status = 'running'
    );

-- name: GetWorkflowStep :one
-- The authorizer of UpdateWorkflowStep reads the step to find its run.
SELECT * FROM workflow_steps WHERE id = @id;

-- name: UpdateWorkflowStep :execrows
-- A reported or failed step stamps finished_at; started_at is set once, at
-- the insert. The param stays uncast so sqlc ties it to the nullable column
-- and applies the NullRawMessage override. reused only sets the flag: a
-- report from a step that a resume reused must not clear it. The write also
-- stamps the run's updated_at, so a report to an interrupted run restarts the
-- run's retention clock in DeleteOldWorkflowRuns. The step update joins the
-- run update, so the run row is locked before the step row, the same order
-- as the purge's cascade. Without the join Postgres may run the two updates
-- in any order, and a report racing the purge could deadlock. owner_id
-- fences the write of the engine; the workflow_report tool passes NULL, so a
-- report reaches the row whoever owns the run. A reported row keeps its
-- value against every engine write, and against a later report while the
-- run is running, because the owner wakes the step on the first report. A
-- report to an interrupted run replaces it. A write that changes nothing
-- returns 0 rows.
WITH run AS (
    UPDATE workflow_runs
    SET updated_at = now()
    WHERE workflow_runs.id = (SELECT run_id FROM workflow_steps WHERE workflow_steps.id = @id)
        AND (sqlc.narg('owner_id')::uuid IS NULL
            OR (workflow_runs.owner_id = sqlc.narg('owner_id')::uuid AND workflow_runs.status = 'running'))
    RETURNING workflow_runs.id, workflow_runs.status
)
UPDATE workflow_steps
SET status = @status, value = @value, error = @error,
    reused = workflow_steps.reused OR @reused,
    finished_at = CASE WHEN @status::text IN ('reported', 'failed') THEN now() ELSE NULL END
FROM run
WHERE workflow_steps.id = @id
    AND workflow_steps.run_id = run.id
    AND (workflow_steps.status <> 'reported'
        OR (sqlc.narg('owner_id')::uuid IS NULL AND run.status <> 'running'));

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
WHERE run_id = @run_id AND status IN ('running', 'reminded')
    AND (sqlc.narg('owner_id')::uuid IS NULL OR EXISTS (
        SELECT 1 FROM workflow_runs
        WHERE workflow_runs.id = @run_id
            AND workflow_runs.owner_id = sqlc.narg('owner_id')::uuid
            AND workflow_runs.status = 'running'
    ));

-- name: DeleteOldWorkflowRuns :execrows
-- Deletes a batch of terminal and interrupted runs whose last journal write
-- is older than 30 days, on the database clock. Their steps cascade. A
-- running run stays: a live replica still writes it. The DELETE repeats the
-- filter: a run that a resume or a step report updates while the DELETE
-- waits for its lock is rechecked on its new row, so it stays.
WITH old AS (
    SELECT id
    FROM workflow_runs
    WHERE status IN ('completed', 'failed', 'stopped', 'interrupted')
        AND updated_at < now() - interval '30 days'
    ORDER BY updated_at
    LIMIT @limit_count::int
)
DELETE FROM workflow_runs
USING old
WHERE workflow_runs.id = old.id
    AND workflow_runs.status IN ('completed', 'failed', 'stopped', 'interrupted')
    AND workflow_runs.updated_at < now() - interval '30 days';

-- name: SetWorkflowRunLogs :exec
-- The engine writes the log lines of a live run through to the journal, capped
-- to the last maxWorkflowLogs lines. lines replaces the retained lines when
-- it is at least as new as the stored sequence, so a stalled write of an
-- older snapshot cannot replace newer lines. The caller keeps the sequence in
-- the lines payload under the fork_seq key. owner_id fences the write to
-- the owner without the running check: the terminal flush follows the
-- terminal status write of the same owner.
UPDATE workflow_runs
SET logs = sqlc.arg(lines)::jsonb, updated_at = now()
WHERE id = @id
	AND (sqlc.narg('owner_id')::uuid IS NULL OR owner_id = sqlc.narg('owner_id')::uuid)
	AND (
		logs IS NULL
		OR logs = 'null'::jsonb
		OR COALESCE(logs->'fork_seq', '0'::jsonb)::bigint <= sqlc.arg(fork_seq)::bigint
	);
