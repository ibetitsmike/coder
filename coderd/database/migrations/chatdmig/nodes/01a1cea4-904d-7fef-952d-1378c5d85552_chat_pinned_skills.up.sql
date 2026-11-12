-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

CREATE TABLE chat_pinned_skills (
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    skill_source text NOT NULL CHECK (skill_source IN ('personal', 'org', 'project', 'workspace')),
    skill_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, skill_source, skill_name)
);

COMMENT ON TABLE chat_pinned_skills IS 'Skills pinned to a chat. chatd puts their bodies in the prompt of every turn until they are unpinned.';
