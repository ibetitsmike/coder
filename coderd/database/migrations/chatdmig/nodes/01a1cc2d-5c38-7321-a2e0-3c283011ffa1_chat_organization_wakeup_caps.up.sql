-- requires: 01a1c28c-8c16-78c2-af7b-26d1d9f7986e_chat_wakeups

CREATE TABLE chat_organization_wakeup_caps (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    max_chat_runs_per_day integer CHECK (max_chat_runs_per_day >= 1),
    max_organization_runs_per_day integer CHECK (max_organization_runs_per_day >= 1),
    max_chat_spend_micros_per_day bigint CHECK (max_chat_spend_micros_per_day >= 1),
    max_organization_spend_micros_per_day bigint CHECK (max_organization_spend_micros_per_day >= 1),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE chat_organization_wakeup_caps IS 'Daily wake-up caps of an organization. A NULL cap uses the built-in default.';
