-- requires: 01a1c242-decb-7db2-b52e-38c2843c434c_agent_definition_acl_org_member_delete

CREATE FUNCTION enforce_agent_definition_project_organization() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    -- A project-tier row also stores its organization, so the two must
    -- agree. Otherwise a row could be read through an organization that
    -- does not own its project.
    IF NEW.project_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM chat_projects
        WHERE id = NEW.project_id AND organization_id = NEW.organization_id
    ) THEN
        RAISE EXCEPTION 'agent definition project is not in its organization'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'agent_definitions_project_organization';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trigger_agent_definition_project_organization
    BEFORE INSERT OR UPDATE OF organization_id, project_id ON agent_definitions
    FOR EACH ROW
    EXECUTE FUNCTION enforce_agent_definition_project_organization();
