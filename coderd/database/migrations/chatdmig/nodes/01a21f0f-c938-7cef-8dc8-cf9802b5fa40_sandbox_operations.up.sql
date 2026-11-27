-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

-- One durable intent row per workspace build that the sandbox provisioner of
-- the fork-sandbox-provisioner experiment runs. The engine writes the intent
-- before it calls the container runtime, so the next build of the workspace
-- sees what a crashed worker left behind. The generation is the build
-- number: only the newest generation of a workspace may claim or complete its
-- operation, so a worker that a newer build superseded cannot finish.
CREATE TABLE sandbox_operations (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    workspace_build_id uuid NOT NULL UNIQUE REFERENCES workspace_builds(id) ON DELETE CASCADE,
    transition workspace_transition NOT NULL,
    generation bigint NOT NULL,
    status text NOT NULL DEFAULT 'intent' CHECK (status IN ('intent', 'claimed', 'done', 'failed', 'superseded')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sandbox_operations_workspace_generation_idx
    ON sandbox_operations (workspace_id, generation);
