-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

-- The durable journal of the fork-workflows experiment. A workflow run is a
-- Starlark program (chatd fork_workflow.go) whose steps are child chats. The
-- registry holds a run in memory on one replica; these tables let a restart
-- resume a run and replay its finished steps without running them again.
CREATE TABLE workflow_runs (
    -- The run ID, derived deterministically from the starting tool call, and
    -- reused as resume_run_id.
    id uuid PRIMARY KEY,
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    model_config_id uuid NOT NULL,
    script text NOT NULL,
    args jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- running: a live replica executes the script. interrupted: a restart
    -- marked every running row before any resume. completed, failed:
    -- terminal. stopped: stop_workflow, a later piece of the work.
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'interrupted', 'completed', 'failed', 'stopped')),
    error text NOT NULL DEFAULT '',
    -- The full result snapshot (status, result, steps, logs) of a terminal
    -- run, or NULL while the run is open.
    result jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX workflow_runs_chat_id_idx ON workflow_runs(chat_id);

-- One row per step (child chat) of a run. The step ID is derived
-- deterministically from the run ID and the step spec, and it is the child
-- chat ID, so a resumed run finds its journal rows again. ordinal is the
-- creation order of the step inside the run.
CREATE TABLE workflow_steps (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    spec_key text NOT NULL,
    occurrence int NOT NULL,
    ordinal int NOT NULL,
    title text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'reminded', 'reported', 'failed')),
    value jsonb,
    error text NOT NULL DEFAULT '',
    UNIQUE (run_id, spec_key, occurrence)
);

COMMENT ON TABLE workflow_runs IS 'Workflow runs of the fork-workflows experiment. chatd journals run state here so a restart resumes a run.';
COMMENT ON TABLE workflow_steps IS 'Workflow steps (child chats) of a fork-workflows run. Finished steps replay from this table without running again.';
