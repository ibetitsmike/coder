-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1bd31-6117-7cbd-bb14-1a8ca7a596b3_chat_subagents


CREATE TABLE chat_organization_subagent_limits (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    max_depth integer CHECK (max_depth BETWEEN 1 AND 16),
    max_concurrent integer CHECK (max_concurrent >= 1),
    max_concurrent_per_family integer CHECK (max_concurrent_per_family >= 1),
    max_live_per_family integer CHECK (max_live_per_family >= 1),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE chat_organization_subagent_limits IS 'Subagent limits of an organization. A NULL limit uses the deployment value, and a set limit cannot raise it.';
