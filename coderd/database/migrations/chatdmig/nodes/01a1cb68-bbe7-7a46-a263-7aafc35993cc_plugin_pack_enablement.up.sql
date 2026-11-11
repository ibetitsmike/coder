-- requires: 01a1cb1c-5809-7422-aefd-a4ced9de57ba_plugin_pack_sources

-- Enablement rows store the organization of the pack, which must also own
-- the project or chat, so the schema keeps a pack from reaching another
-- organization. The pack side is a foreign key; chat_projects and chats are
-- upstream tables, so the project and chat side is a trigger, as in
-- agent_definition_project_organization.
ALTER TABLE plugin_packs ADD CONSTRAINT plugin_packs_id_organization_id_key UNIQUE (id, organization_id);

CREATE TABLE plugin_pack_projects (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES chat_projects(id) ON DELETE CASCADE,
    pack_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, pack_id),
    FOREIGN KEY (pack_id, organization_id) REFERENCES plugin_packs(id, organization_id) ON DELETE CASCADE
);

CREATE INDEX plugin_pack_projects_pack_id_idx ON plugin_pack_projects (pack_id);

COMMENT ON TABLE plugin_pack_projects IS 'Plugin packs enabled for every chat of a chat project.';

CREATE TABLE chat_plugin_packs (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    pack_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, pack_id),
    FOREIGN KEY (pack_id, organization_id) REFERENCES plugin_packs(id, organization_id) ON DELETE CASCADE
);

CREATE INDEX chat_plugin_packs_pack_id_idx ON chat_plugin_packs (pack_id);

COMMENT ON TABLE chat_plugin_packs IS 'Plugin packs enabled for one chat, in addition to the packs of its project.';

CREATE FUNCTION enforce_plugin_pack_project_organization() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM chat_projects
        WHERE id = NEW.project_id AND organization_id = NEW.organization_id
    ) THEN
        RAISE EXCEPTION 'plugin pack project is not in the pack organization'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'plugin_pack_projects_organization';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION enforce_chat_plugin_pack_organization() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM chats
        WHERE id = NEW.chat_id AND organization_id = NEW.organization_id
    ) THEN
        RAISE EXCEPTION 'plugin pack chat is not in the pack organization'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'chat_plugin_packs_organization';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trigger_plugin_pack_projects_organization
    BEFORE INSERT OR UPDATE ON plugin_pack_projects
    FOR EACH ROW
    EXECUTE FUNCTION enforce_plugin_pack_project_organization();

CREATE TRIGGER trigger_chat_plugin_packs_organization
    BEFORE INSERT OR UPDATE ON chat_plugin_packs
    FOR EACH ROW
    EXECUTE FUNCTION enforce_chat_plugin_pack_organization();
