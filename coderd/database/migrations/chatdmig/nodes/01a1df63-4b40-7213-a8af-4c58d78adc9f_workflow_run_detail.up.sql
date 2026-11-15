-- requires: 01a1d041-1628-77af-9c39-d4e3e711e0d3_workflow_journal
-- requires: 01a1d05b-a4aa-7050-ac85-2f3c1e806fe9_workflow_run_timestamps
-- requires: 01a1da95-80da-7dd4-a368-4b8bf0e1f65f_chat_descendant_questions_answered

-- The run detail of the fork-workflows experiment reads what a run was
-- called with, whether a step was reused, and the log lines of a live run:
-- the journal only kept the args, the reuse flag and the logs inside the
-- result snapshot of a terminal run before.

ALTER TABLE workflow_runs
    ADD COLUMN logs jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE workflow_steps
    ADD COLUMN reused boolean NOT NULL DEFAULT false;

-- The old journal kept the reuse flag only inside the result snapshot of a
-- terminal run, keyed by the step's chat ID. Steps that a resume replayed
-- read as reused again; every other row keeps the default. No row is deleted
-- and no status changes.
UPDATE workflow_steps s
SET reused = true
FROM workflow_runs r
WHERE r.id = s.run_id
    AND jsonb_path_exists(
        r.result -> 'steps',
        '$[*] ? (@.chat_id == $id && @.reused == true)',
        jsonb_build_object('id', s.id::text));

COMMENT ON COLUMN workflow_runs.logs IS 'The last log lines of a live run, written through as the script logs. The terminal snapshot of result replaces them.';
COMMENT ON COLUMN workflow_steps.reused IS 'True when a resumed run replayed this step instead of running it again.';