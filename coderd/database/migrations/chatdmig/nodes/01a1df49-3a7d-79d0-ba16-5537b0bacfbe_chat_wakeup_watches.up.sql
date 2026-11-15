-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1c28c-8c16-78c2-af7b-26d1d9f7986e_chat_wakeups
-- requires: 01a1da95-80da-7dd4-a368-4b8bf0e1f65f_chat_descendant_questions_answered

CREATE TYPE chat_wakeup_watch_kind AS ENUM ('child_done', 'process', 'pull_request');

-- One row per thing that an events wake-up watches: a child agent, a
-- background process or a pull request.
CREATE TABLE chat_wakeup_watches (
    id uuid PRIMARY KEY,
    wakeup_id uuid NOT NULL REFERENCES chat_wakeups(id) ON DELETE CASCADE,
    -- The chat to wake, copied from the wake-up.
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind chat_wakeup_watch_kind NOT NULL,
    -- The position of the watch in its wake-up, which delivers in this order.
    ordinal integer NOT NULL,
    -- The target, one per kind. A child that is deleted takes its watch with
    -- it: it has nothing to report.
    child_chat_id uuid REFERENCES chats(id) ON DELETE CASCADE,
    -- The workspace agent that runs process_id. Not a foreign key: a rebuild
    -- replaces the agent, and the watch then reports the process as gone.
    agent_id uuid,
    process_id text,
    pull_request_url text,
    -- Options and baselines of the kind: the command and output pattern of a
    -- process, the events, head SHA and seen activity of a pull request.
    spec jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Set by whoever observed the event, for the watches that a poll does
    -- not read. A child_done watch reads its child from chats instead.
    fired_at timestamptz,
    fired_payload jsonb,
    -- Set in the transaction that inserts the message. A delivered watch is
    -- done.
    delivered_at timestamptz,
    -- How many messages the watch sent. Process output lines send more than
    -- one before the exit.
    deliveries integer NOT NULL DEFAULT 0,
    -- When a send of the watch failed since its last delivery, so the final
    -- poll after the expiry still retries it.
    failed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chat_wakeup_watches_wakeup_ordinal UNIQUE (wakeup_id, ordinal),
    CONSTRAINT chat_wakeup_watches_child_target CHECK ((kind = 'child_done') = (child_chat_id IS NOT NULL)),
    CONSTRAINT chat_wakeup_watches_process_target CHECK ((kind = 'process') = (process_id IS NOT NULL)),
    CONSTRAINT chat_wakeup_watches_pull_request_target CHECK ((kind = 'pull_request') = (pull_request_url IS NOT NULL)),
    CONSTRAINT chat_wakeup_watches_deliveries_nonnegative CHECK (deliveries >= 0)
);
-- The open watches. Only live waits have one, so the query that finds the
-- ready watches of the whole deployment starts from a small index.
CREATE INDEX chat_wakeup_watches_open_wakeup_idx ON chat_wakeup_watches (wakeup_id) WHERE delivered_at IS NULL;
CREATE INDEX chat_wakeup_watches_chat_id_idx ON chat_wakeup_watches (chat_id);
CREATE INDEX chat_wakeup_watches_child_chat_id_idx ON chat_wakeup_watches (child_chat_id);
CREATE INDEX chat_wakeup_watches_organization_id_idx ON chat_wakeup_watches (organization_id);
