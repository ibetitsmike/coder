-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

CREATE TABLE chat_organization_token_budget (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    strategy text NOT NULL CHECK (strategy IN ('summary', 'token_budget')),
    fallback text NOT NULL CHECK (fallback IN ('new_window', 'summary')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE chat_organization_token_budget IS 'Compaction strategy of the fork-token-budget experiment for an organization. Without a row the organization uses summary compaction, and the fallback is a new context window.';
