-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

-- One durable intent row per container operation the sandbox provisioner
-- runs. The engine inserts an intent before it drives the container
-- runtime, so a restart sees what a crashed worker left behind. The
-- generation fences a superseded worker: only the newest generation of a
-- workspace claims its operation.
--
-- workspace_build_id is informational and carries no foreign key: the
-- workspace row owns cleanup, and an operation inserts only while its
-- build row exists.
CREATE TABLE sandbox_operations (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    workspace_build_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('create', 'stop', 'destroy')),
    generation bigint NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'intent' CHECK (status IN ('intent', 'claimed', 'failed', 'done')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sandbox_operations_workspace_build_id_idx
    ON sandbox_operations (workspace_build_id);
CREATE INDEX sandbox_operations_workspace_generation_idx
    ON sandbox_operations (workspace_id, generation);