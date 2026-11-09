-- requires: 01a1c18d-027a-7d4a-a2ad-e823d1e3ca6c_agent_definitions

CREATE FUNCTION delete_agent_definition_user_acl_on_org_member_delete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    -- Drop the departing member's direct grants so adding them back to the
    -- organization does not silently restore access, like
    -- delete_skill_user_acl_on_org_member_delete.
    UPDATE agent_definitions
    SET
        user_acl = user_acl - OLD.user_id::text,
        updated_at = now()
    WHERE organization_id = OLD.organization_id AND user_acl ? OLD.user_id::text;
    RETURN OLD;
END;
$$;

CREATE TRIGGER trigger_delete_agent_definition_user_acl_on_org_member_delete
    BEFORE DELETE ON public.organization_members
    FOR EACH ROW
    EXECUTE FUNCTION delete_agent_definition_user_acl_on_org_member_delete();

-- Drop grants for users who already left the definition's organization.
UPDATE agent_definitions
SET user_acl = (
    SELECT COALESCE(jsonb_object_agg(entry.key, entry.value), '{}'::jsonb)
    FROM jsonb_each(agent_definitions.user_acl) AS entry
    WHERE EXISTS (
        SELECT 1
        FROM organization_members
        WHERE organization_members.organization_id = agent_definitions.organization_id
            AND organization_members.user_id::text = entry.key
    )
)
WHERE user_acl <> '{}'::jsonb;
