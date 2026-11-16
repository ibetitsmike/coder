-- requires: 01a1d041-1628-77af-9c39-d4e3e711e0d3_workflow_journal

-- The run API of the fork-workflows experiment (list, detail, stop) reads
-- timestamps: when a run or a step started and ended. updated_at orders the
-- keyset pagination of the run list and tells the UI that a polled run
-- changed.

ALTER TABLE workflow_runs
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN finished_at timestamptz;

ALTER TABLE workflow_steps
    ADD COLUMN started_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN finished_at timestamptz;

-- Terminal runs and finished steps keep their end time; an interrupted run
-- stays open, so its finished_at stays NULL until a resume ends it.
CREATE INDEX workflow_runs_chat_created_idx ON workflow_runs(chat_id, created_at DESC, id DESC);