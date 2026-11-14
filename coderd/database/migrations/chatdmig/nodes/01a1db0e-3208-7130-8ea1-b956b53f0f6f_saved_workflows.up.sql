-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

-- Saved Starlark workflows of the fork-workflows experiment. The tiers mirror
-- agent_definitions: a user-tier row belongs to one user in one organization,
-- and an organization-tier row is shared through group_acl and user_acl.
CREATE TABLE saved_workflows (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id uuid REFERENCES users(id) ON DELETE CASCADE,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    script text NOT NULL,
    args_schema jsonb,
    phases text[] NOT NULL DEFAULT '{}',
    group_acl jsonb NOT NULL DEFAULT '{}'::jsonb,
    user_acl jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT saved_workflows_name_format CHECK (name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT saved_workflows_name_size CHECK (octet_length(name) <= 128),
    CONSTRAINT saved_workflows_description_size CHECK (octet_length(description) <= 4096),
    CONSTRAINT saved_workflows_script_size CHECK (octet_length(script) <= 65536),
    CONSTRAINT saved_workflows_args_schema_is_object CHECK (args_schema IS NULL OR jsonb_typeof(args_schema) = 'object'),
    CONSTRAINT saved_workflows_args_schema_size CHECK (args_schema IS NULL OR octet_length(args_schema::text) <= 16384),
    CONSTRAINT saved_workflows_phases_size CHECK (cardinality(phases) <= 32),
    CONSTRAINT saved_workflows_group_acl_is_object CHECK (jsonb_typeof(group_acl) = 'object'),
    CONSTRAINT saved_workflows_user_acl_is_object CHECK (jsonb_typeof(user_acl) = 'object'),
    CONSTRAINT saved_workflows_acl_only_on_organization_tier CHECK (user_id IS NULL OR (group_acl = '{}'::jsonb AND user_acl = '{}'::jsonb))
);

CREATE UNIQUE INDEX saved_workflows_user_name_idx ON saved_workflows (user_id, organization_id, name) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX saved_workflows_organization_name_idx ON saved_workflows (organization_id, name) WHERE user_id IS NULL;
CREATE INDEX saved_workflows_organization_id_idx ON saved_workflows (organization_id);
CREATE INDEX saved_workflows_created_by_idx ON saved_workflows (created_by);

CREATE FUNCTION enforce_saved_workflows_per_owner_limit() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    workflow_count int;
    workflow_limit constant int := 100;
BEGIN
    -- Serialize the checks per owner so concurrent inserts cannot all observe
    -- the same pre-insert count, like enforce_agent_definitions_per_owner_limit.
    IF NEW.user_id IS NOT NULL THEN
        PERFORM 1 FROM users WHERE id = NEW.user_id FOR NO KEY UPDATE;
        SELECT count(*) INTO workflow_count FROM saved_workflows
        WHERE organization_id = NEW.organization_id AND user_id = NEW.user_id;
        IF workflow_count >= workflow_limit THEN
            RAISE EXCEPTION 'user has reached the saved workflow limit of the organization'
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'saved_workflows_per_user_limit';
        END IF;
    ELSE
        PERFORM 1 FROM organizations WHERE id = NEW.organization_id FOR NO KEY UPDATE;
        SELECT count(*) INTO workflow_count FROM saved_workflows
        WHERE organization_id = NEW.organization_id AND user_id IS NULL;
        IF workflow_count >= workflow_limit THEN
            RAISE EXCEPTION 'organization has reached the saved workflow limit'
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'saved_workflows_per_organization_limit';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trigger_saved_workflows_per_owner_limit BEFORE INSERT ON saved_workflows FOR EACH ROW EXECUTE FUNCTION enforce_saved_workflows_per_owner_limit();

CREATE FUNCTION delete_saved_workflow_user_acl_on_org_member_delete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    -- Drop the departing member's direct grants so adding them back to the
    -- organization does not silently restore access, like
    -- delete_agent_definition_user_acl_on_org_member_delete.
    UPDATE saved_workflows
    SET
        user_acl = user_acl - OLD.user_id::text,
        updated_at = now()
    WHERE organization_id = OLD.organization_id
        AND user_id IS NULL
        AND user_acl ? OLD.user_id::text;
    RETURN OLD;
END;
$$;

CREATE TRIGGER trigger_delete_saved_workflow_user_acl_on_org_member_delete
    BEFORE DELETE ON public.organization_members
    FOR EACH ROW
    EXECUTE FUNCTION delete_saved_workflow_user_acl_on_org_member_delete();

COMMENT ON TABLE saved_workflows IS 'Saved Starlark workflows of the fork-workflows experiment. A row is in the organization tier when user_id is NULL and in the user tier when user_id is set. Only users save workflows; agents run them by name.';
COMMENT ON COLUMN saved_workflows.user_id IS 'The owner of a user-tier row. A user-tier row also belongs to one organization of the user.';
COMMENT ON COLUMN saved_workflows.created_by IS 'The user who saved the workflow. NULL when that user was deleted.';
COMMENT ON COLUMN saved_workflows.script IS 'The Starlark source. It defines main(args), as a run_workflow script does.';
COMMENT ON COLUMN saved_workflows.args_schema IS 'An optional JSON Schema object for the args of a run.';
COMMENT ON COLUMN saved_workflows.phases IS 'The declared phase names of the workflow, in order. Empty when it declares none.';
COMMENT ON COLUMN saved_workflows.group_acl IS 'Group permissions on an organization-tier row. Empty on the user tier.';
COMMENT ON COLUMN saved_workflows.user_acl IS 'User permissions on an organization-tier row. Empty on the user tier.';
