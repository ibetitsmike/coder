-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1c18d-027a-7d4a-a2ad-e823d1e3ca6c_agent_definitions

CREATE TYPE chat_wakeup_kind AS ENUM ('delay', 'interval', 'cron', 'events');
CREATE TYPE chat_wakeup_status AS ENUM ('active', 'paused', 'completed', 'canceled');
CREATE TYPE chat_wakeup_run_status AS ENUM ('pending', 'delivered', 'skipped', 'failed');

CREATE TABLE chat_wakeups (
    id uuid PRIMARY KEY,
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    -- The organization of the chat, copied when the wake-up is created.
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- The user whose action created it (the chat owner for tool calls).
    created_by uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- What created it: 'schedule_wakeup', 'loop' or 'wait_for_events'.
    source text NOT NULL,
    kind chat_wakeup_kind NOT NULL,
    -- The user message each run sends to the chat.
    prompt text NOT NULL,
    interval_seconds integer,
    cron text,
    -- IANA time zone of cron.
    timezone text NOT NULL DEFAULT 'UTC',
    -- For kind 'events': JSON array of event filters the poller checks.
    events jsonb NOT NULL DEFAULT '[]'::jsonb,
    status chat_wakeup_status NOT NULL DEFAULT 'active',
    -- Why it is paused or canceled, e.g. 'user', 'chat_daily_cap', 'org_daily_cap', 'spend_cap', 'hook_denied', 'archived'.
    status_reason text,
    -- Next time the worker delivers (or polls, for 'events'). NULL when not active.
    next_run_at timestamptz,
    -- A worker that claimed the row owns it until this time.
    lease_expires_at timestamptz,
    expires_at timestamptz,
    max_runs integer,
    run_count integer NOT NULL DEFAULT 0,
    last_run_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chat_wakeups_interval_shape CHECK ((kind = 'interval') = (interval_seconds IS NOT NULL)),
    CONSTRAINT chat_wakeups_interval_positive CHECK (interval_seconds IS NULL OR interval_seconds >= 60),
    CONSTRAINT chat_wakeups_cron_shape CHECK ((kind = 'cron') = (cron IS NOT NULL)),
    CONSTRAINT chat_wakeups_max_runs_positive CHECK (max_runs IS NULL OR max_runs > 0),
    CONSTRAINT chat_wakeups_active_has_next_run CHECK (status <> 'active' OR next_run_at IS NOT NULL)
);
CREATE INDEX chat_wakeups_due_idx ON chat_wakeups (next_run_at) WHERE status = 'active';
CREATE INDEX chat_wakeups_chat_id_idx ON chat_wakeups (chat_id);
CREATE INDEX chat_wakeups_organization_id_idx ON chat_wakeups (organization_id);

-- One row per delivery attempt. Kept when its wake-up is deleted, so daily
-- caps still count it.
CREATE TABLE chat_wakeup_runs (
    id uuid PRIMARY KEY,
    wakeup_id uuid REFERENCES chat_wakeups(id) ON DELETE SET NULL,
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- Identifies the occurrence: the scheduled time (RFC 3339) or an event key.
    run_key text NOT NULL,
    status chat_wakeup_run_status NOT NULL DEFAULT 'pending',
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chat_wakeup_runs_wakeup_run_key UNIQUE (wakeup_id, run_key)
);
CREATE INDEX chat_wakeup_runs_chat_created_idx ON chat_wakeup_runs (chat_id, created_at);
CREATE INDEX chat_wakeup_runs_org_created_idx ON chat_wakeup_runs (organization_id, created_at);
