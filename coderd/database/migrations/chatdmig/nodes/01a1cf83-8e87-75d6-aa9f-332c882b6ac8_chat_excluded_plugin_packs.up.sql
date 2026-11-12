-- requires: 01a1cb68-bbe7-7a46-a263-7aafc35993cc_plugin_pack_enablement

CREATE TABLE chat_excluded_plugin_packs (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    pack_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, pack_id),
    FOREIGN KEY (pack_id, organization_id) REFERENCES plugin_packs(id, organization_id) ON DELETE CASCADE
);

CREATE INDEX chat_excluded_plugin_packs_pack_id_idx ON chat_excluded_plugin_packs (pack_id);

COMMENT ON TABLE chat_excluded_plugin_packs IS 'Plugin packs turned off for one chat and its subagents, even when its project or the chat enables them.';

CREATE FUNCTION enforce_chat_excluded_plugin_pack_organization() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM chats
        WHERE id = NEW.chat_id AND organization_id = NEW.organization_id
    ) THEN
        RAISE EXCEPTION 'excluded plugin pack chat is not in the pack organization'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'chat_excluded_plugin_packs_organization';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trigger_chat_excluded_plugin_packs_organization
    BEFORE INSERT OR UPDATE ON chat_excluded_plugin_packs
    FOR EACH ROW
    EXECUTE FUNCTION enforce_chat_excluded_plugin_pack_organization();
