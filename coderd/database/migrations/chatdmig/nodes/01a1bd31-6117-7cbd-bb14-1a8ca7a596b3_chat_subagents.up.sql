-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

-- One row per subagent chat that the fork tracks. A child chat without a
-- row is a legacy shared child of its built-in type.
CREATE TABLE chat_subagents (
    chat_id uuid PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    -- general, explore or a custom agent type ID.
    agent_type text NOT NULL,
    depth int NOT NULL DEFAULT 1,
    isolation text NOT NULL DEFAULT 'shared' CHECK (isolation IN ('shared', 'worktree', 'workspace')),
    worktree_path text NOT NULL DEFAULT '',
    branch text NOT NULL DEFAULT '',
    workspace_id uuid REFERENCES workspaces(id) ON DELETE SET NULL
);
