-- API key scopes for the plugin RBAC resource and the composite scope that
-- plugin chat bridges mint for linked users.
ALTER TYPE api_key_scope ADD VALUE IF NOT EXISTS 'plugin:*';
ALTER TYPE api_key_scope ADD VALUE IF NOT EXISTS 'plugin:create';
ALTER TYPE api_key_scope ADD VALUE IF NOT EXISTS 'plugin:read';
ALTER TYPE api_key_scope ADD VALUE IF NOT EXISTS 'plugin:update';
ALTER TYPE api_key_scope ADD VALUE IF NOT EXISTS 'plugin:delete';
ALTER TYPE api_key_scope ADD VALUE IF NOT EXISTS 'coder:chats.converse';

CREATE TABLE plugins (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name text NOT NULL,
    version text NOT NULL DEFAULT '',
    file_id uuid NOT NULL REFERENCES files(id),
    manifest jsonb NOT NULL,
    grants jsonb NOT NULL,
    config text NOT NULL DEFAULT '',
    config_key_id text REFERENCES dbcrypt_keys(active_key_digest),
    enabled boolean NOT NULL DEFAULT true,
    runner_id uuid,
    lease_expires_at timestamp with time zone,
    last_error text NOT NULL DEFAULT '',
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

COMMENT ON COLUMN plugins.manifest IS 'Capabilities the plugin module requests.';
COMMENT ON COLUMN plugins.grants IS 'Capabilities the owner granted to the plugin.';
COMMENT ON COLUMN plugins.config IS 'JSON object of string to string. Encrypted by dbcrypt when config_key_id is set.';
COMMENT ON COLUMN plugins.runner_id IS 'Replica that holds the lease to run the plugin.';

CREATE TABLE plugin_user_links (
    plugin_id uuid NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    external_id text NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    active_chat_id uuid REFERENCES chats(id) ON DELETE SET NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin_id, external_id)
);

CREATE INDEX plugin_user_links_user_id_idx ON plugin_user_links (user_id);

COMMENT ON COLUMN plugin_user_links.external_id IS 'Identity of the user on the external service, for example a phone number.';
COMMENT ON COLUMN plugin_user_links.active_chat_id IS 'Chat that last spoke to the external identity. Replies go to this chat.';

CREATE TABLE plugin_link_codes (
    code_hash bytea PRIMARY KEY,
    plugin_id uuid NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

COMMENT ON COLUMN plugin_link_codes.code_hash IS 'SHA-256 of the link code.';

CREATE TABLE plugin_chat_bindings (
    plugin_id uuid NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    sent_message_id bigint NOT NULL,
    sent_status chat_status,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin_id, chat_id)
);

COMMENT ON COLUMN plugin_chat_bindings.sent_message_id IS 'Highest chat_messages.id delivered to the external service.';
COMMENT ON COLUMN plugin_chat_bindings.sent_status IS 'Chat status last delivered to the external service.';
