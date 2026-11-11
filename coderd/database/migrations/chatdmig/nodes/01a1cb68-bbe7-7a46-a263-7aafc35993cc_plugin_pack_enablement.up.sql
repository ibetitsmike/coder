-- requires: 01a1cb1c-5809-7422-aefd-a4ced9de57ba_plugin_pack_sources

CREATE TABLE plugin_pack_projects (
    project_id uuid NOT NULL REFERENCES chat_projects(id) ON DELETE CASCADE,
    pack_id uuid NOT NULL REFERENCES plugin_packs(id) ON DELETE CASCADE,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, pack_id)
);

CREATE INDEX plugin_pack_projects_pack_id_idx ON plugin_pack_projects (pack_id);

COMMENT ON TABLE plugin_pack_projects IS 'Plugin packs enabled for every chat of a chat project.';

CREATE TABLE chat_plugin_packs (
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    pack_id uuid NOT NULL REFERENCES plugin_packs(id) ON DELETE CASCADE,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, pack_id)
);

CREATE INDEX chat_plugin_packs_pack_id_idx ON chat_plugin_packs (pack_id);

COMMENT ON TABLE chat_plugin_packs IS 'Plugin packs enabled for one chat, in addition to the packs of its project.';
