-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1df66-ebee-7c93-aa87-3edc1f2d8fb0_workflow_phases
-- requires: 01a1da95-80da-7dd4-a368-4b8bf0e1f65f_chat_descendant_questions_answered

-- The run detail of the fork-workflows experiment reads what a run was
-- called with, whether a step was reused, and the log lines of a live run:
-- the journal only kept the args, the reuse flag and the logs inside the
-- result snapshot of a terminal run before. It replaces the earlier
-- 01a1df63 node, which was never applied on a shared database and did not
-- order against the phases node: both added columns to the same tables, so
-- the column order differed between an upgraded and a fresh database.

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

COMMENT ON COLUMN workflow_runs.logs IS 'The last log lines of a live run with their journal sequence under fork_seq, written through as the script logs. The terminal snapshot of result replaces them.';
COMMENT ON COLUMN workflow_steps.reused IS 'True when a resumed run replayed this step instead of running it again.';