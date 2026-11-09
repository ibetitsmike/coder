-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

CREATE TABLE agent_definitions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id uuid REFERENCES users(id) ON DELETE CASCADE,
    project_id uuid REFERENCES chat_projects(id) ON DELETE CASCADE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    content text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    group_acl jsonb NOT NULL DEFAULT '{}'::jsonb,
    user_acl jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT agent_definitions_single_tier CHECK (num_nonnulls(user_id, project_id) <= 1),
    CONSTRAINT agent_definitions_name_format CHECK (name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT agent_definitions_name_size CHECK (octet_length(name) <= 256),
    CONSTRAINT agent_definitions_description_size CHECK (octet_length(description) <= 4096),
    CONSTRAINT agent_definitions_content_size CHECK (octet_length(content) <= 65536),
    CONSTRAINT agent_definitions_group_acl_is_object CHECK (jsonb_typeof(group_acl) = 'object'),
    CONSTRAINT agent_definitions_user_acl_is_object CHECK (jsonb_typeof(user_acl) = 'object'),
    CONSTRAINT agent_definitions_acl_only_on_organization_tier CHECK ((user_id IS NULL AND project_id IS NULL) OR (group_acl = '{}'::jsonb AND user_acl = '{}'::jsonb))
);

CREATE UNIQUE INDEX agent_definitions_user_name_idx ON agent_definitions (user_id, organization_id, name) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX agent_definitions_organization_name_idx ON agent_definitions (organization_id, name) WHERE user_id IS NULL AND project_id IS NULL;
CREATE UNIQUE INDEX agent_definitions_project_name_idx ON agent_definitions (project_id, name) WHERE project_id IS NOT NULL;
CREATE INDEX agent_definitions_organization_id_idx ON agent_definitions (organization_id);

CREATE FUNCTION enforce_agent_definitions_per_owner_limit() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    definition_count int;
    definition_limit constant int := 100;
BEGIN
    -- Serialize the checks per owner so concurrent inserts cannot all observe
    -- the same pre-insert count, like enforce_skills_per_owner_limit.
    IF NEW.user_id IS NOT NULL THEN
        PERFORM 1 FROM users WHERE id = NEW.user_id FOR NO KEY UPDATE;
        SELECT count(*) INTO definition_count FROM agent_definitions
        WHERE organization_id = NEW.organization_id AND user_id = NEW.user_id;
        IF definition_count >= definition_limit THEN
            RAISE EXCEPTION 'user has reached the agent definition limit of the organization'
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'agent_definitions_per_user_limit';
        END IF;
    ELSIF NEW.project_id IS NOT NULL THEN
        PERFORM 1 FROM chat_projects WHERE id = NEW.project_id FOR NO KEY UPDATE;
        SELECT count(*) INTO definition_count FROM agent_definitions WHERE project_id = NEW.project_id;
        IF definition_count >= definition_limit THEN
            RAISE EXCEPTION 'project has reached the agent definition limit'
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'agent_definitions_per_project_limit';
        END IF;
    ELSE
        PERFORM 1 FROM organizations WHERE id = NEW.organization_id FOR NO KEY UPDATE;
        SELECT count(*) INTO definition_count FROM agent_definitions
        WHERE organization_id = NEW.organization_id AND user_id IS NULL AND project_id IS NULL;
        IF definition_count >= definition_limit THEN
            RAISE EXCEPTION 'organization has reached the agent definition limit'
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'agent_definitions_per_organization_limit';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trigger_agent_definitions_per_owner_limit BEFORE INSERT ON agent_definitions FOR EACH ROW EXECUTE FUNCTION enforce_agent_definitions_per_owner_limit();

COMMENT ON TABLE agent_definitions IS 'Stored custom agent definitions. A row is in the organization tier when user_id and project_id are NULL, in the user tier when user_id is set, and in the project tier when project_id is set.';
COMMENT ON COLUMN agent_definitions.user_id IS 'The owner of a user-tier row. Unlike personal skills, a user-tier row also belongs to one organization of the user.';
COMMENT ON COLUMN agent_definitions.content IS 'The agent markdown file: front-matter and prompt body, parsed by workspacesdk.ParseAgentDefinition.';
COMMENT ON COLUMN agent_definitions.enabled IS 'Whether the definition is offered to chats. A disabled definition is kept but not resolved.';
COMMENT ON COLUMN agent_definitions.group_acl IS 'Group permissions on an organization-tier row. Empty on the other tiers.';
COMMENT ON COLUMN agent_definitions.user_acl IS 'User permissions on an organization-tier row. Empty on the other tiers.';
