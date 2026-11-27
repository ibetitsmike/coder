-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

-- One durable intent row per build of the fork-sandbox-provisioner engine.
-- The generation is the build number: only the newest generation of a
-- workspace may claim or complete its operation.
CREATE TABLE sandbox_operations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    workspace_build_id uuid NOT NULL UNIQUE REFERENCES workspace_builds(id) ON DELETE CASCADE,
    transition workspace_transition NOT NULL,
    generation bigint NOT NULL,
    status text NOT NULL DEFAULT 'intent' CHECK (status IN ('intent', 'claimed', 'done', 'failed', 'superseded', 'reconciled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sandbox_operations_workspace_generation_idx
    ON sandbox_operations (workspace_id, generation);
