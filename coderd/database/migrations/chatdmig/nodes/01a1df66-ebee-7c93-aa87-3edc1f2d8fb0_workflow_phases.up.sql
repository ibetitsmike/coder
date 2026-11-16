-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1d05b-a4aa-7050-ac85-2f3c1e806fe9_workflow_run_timestamps

-- Phases of the fork-workflows runs. A script may declare its phases in a
-- top-level PHASES = [...] list and switch between them with phase(name).
-- The engine reads PHASES before main runs, journals the declared list on
-- the run, and tags every step with the phase that was current when the
-- step started. A script may also call phase() with a name that PHASES did
-- not declare: the name is accepted and journaled as an observed phase, so
-- a step can never lose its phase, and the API adds it to the declared
-- list in the order the run reached it. A resumed run keeps both.

ALTER TABLE workflow_runs
    -- The phases of the run, in display order: the declared order first,
    -- then each undeclared name that phase() reached, in first-reach order.
    ADD COLUMN phases text[] NOT NULL DEFAULT '{}',
    -- The phase that was current at the last journal write. Empty until the
    -- script calls phase() the first time.
    ADD COLUMN current_phase text NOT NULL DEFAULT '';

ALTER TABLE workflow_steps
    ADD COLUMN phase text NOT NULL DEFAULT '';
