-- name: InsertPlugin :one
INSERT INTO plugins (
    id,
    organization_id,
    name,
    version,
    file_id,
    manifest,
    grants,
    config,
    config_key_id,
    enabled,
    created_by,
    created_at,
    updated_at
) VALUES (
    @id,
    @organization_id,
    @name,
    @version,
    @file_id,
    @manifest,
    @grants,
    @config,
    @config_key_id,
    @enabled,
    @created_by,
    @created_at,
    @updated_at
)
RETURNING *;

-- name: GetPluginByID :one
SELECT * FROM plugins WHERE id = @id;

-- name: GetPluginsByOrganizationID :many
SELECT * FROM plugins WHERE organization_id = @organization_id ORDER BY name;

-- name: UpdatePluginConfig :one
UPDATE plugins
SET
    config = @config,
    config_key_id = @config_key_id,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: UpdatePluginEnabled :one
UPDATE plugins
SET
    enabled = @enabled,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: UpdatePluginLastError :exec
UPDATE plugins SET last_error = @last_error WHERE id = @id;

-- name: DeletePluginByID :exec
DELETE FROM plugins WHERE id = @id;

-- Leases enabled plugins to a replica. A replica renews its own leases on
-- every call and takes over plugins whose lease expired. SKIP LOCKED keeps
-- concurrent replicas from blocking on, or double leasing, the same rows.
-- name: AcquirePluginLeases :many
UPDATE plugins
SET
    runner_id = @runner_id::uuid,
    lease_expires_at = now() + make_interval(secs => sqlc.arg('lease_seconds')::int)
WHERE id IN (
    SELECT p.id
    FROM plugins p
    WHERE p.enabled
        AND (
            p.runner_id = @runner_id::uuid
            OR p.lease_expires_at IS NULL
            OR p.lease_expires_at < now()
        )
    ORDER BY p.id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg('max_plugins')::int
)
RETURNING *;

-- name: ReleasePluginLeases :exec
UPDATE plugins
SET
    runner_id = NULL,
    lease_expires_at = NULL
WHERE runner_id = @runner_id::uuid;

-- name: InsertPluginLinkCode :exec
INSERT INTO plugin_link_codes (code_hash, plugin_id, user_id, expires_at)
VALUES (@code_hash, @plugin_id, @user_id, @expires_at);

-- name: ConsumePluginLinkCode :one
DELETE FROM plugin_link_codes
WHERE plugin_id = @plugin_id
    AND code_hash = @code_hash
    AND expires_at > now()
RETURNING *;

-- name: DeletePluginLinkCodesByUser :exec
DELETE FROM plugin_link_codes WHERE plugin_id = @plugin_id AND user_id = @user_id;

-- Relinking an external identity to another user clears the active chat so
-- replies never reach a chat the new user does not own.
-- name: UpsertPluginUserLink :one
INSERT INTO plugin_user_links (plugin_id, external_id, user_id)
VALUES (@plugin_id, @external_id, @user_id)
ON CONFLICT (plugin_id, external_id) DO UPDATE
SET
    user_id = EXCLUDED.user_id,
    active_chat_id = NULL,
    created_at = now()
RETURNING *;

-- name: GetPluginUserLink :one
SELECT * FROM plugin_user_links WHERE plugin_id = @plugin_id AND external_id = @external_id;

-- name: GetPluginUserLinksByPluginID :many
SELECT * FROM plugin_user_links WHERE plugin_id = @plugin_id ORDER BY external_id;

-- name: GetPluginUserLinksByUserID :many
SELECT * FROM plugin_user_links WHERE user_id = @user_id ORDER BY plugin_id, external_id;

-- name: GetPluginUserLinksByPluginAndUser :many
SELECT * FROM plugin_user_links
WHERE plugin_id = @plugin_id AND user_id = @user_id
ORDER BY external_id;

-- name: DeletePluginUserLink :exec
DELETE FROM plugin_user_links WHERE plugin_id = @plugin_id AND external_id = @external_id;

-- name: SetPluginUserLinkActiveChat :exec
UPDATE plugin_user_links
SET active_chat_id = @active_chat_id
WHERE plugin_id = @plugin_id AND external_id = @external_id;

-- Binds every top-level, unarchived chat of a linked user in the plugin's
-- organization. Chats created after the link start at cursor 0 so their whole
-- history is delivered. Older chats start at their newest message so linking
-- does not replay history.
-- name: EnsurePluginChatBindings :exec
INSERT INTO plugin_chat_bindings (plugin_id, chat_id, sent_message_id, sent_status)
SELECT DISTINCT ON (c.id)
    l.plugin_id,
    c.id,
    CASE
        WHEN c.created_at >= l.created_at THEN 0
        ELSE COALESCE((SELECT max(m.id) FROM chat_messages m WHERE m.chat_id = c.id), 0)
    END,
    CASE
        WHEN c.created_at >= l.created_at THEN NULL
        ELSE c.status
    END
FROM plugin_user_links l
JOIN plugins p ON p.id = l.plugin_id
JOIN chats c ON c.owner_id = l.user_id AND c.organization_id = p.organization_id
WHERE l.plugin_id = @plugin_id
    AND c.parent_chat_id IS NULL
    AND NOT c.archived
ORDER BY c.id, l.created_at
ON CONFLICT (plugin_id, chat_id) DO NOTHING;

-- A binding is due when its chat is idle and has undelivered user-visible
-- assistant messages or a status that was not delivered yet.
-- name: GetPluginChatBindingsDue :many
SELECT
    b.plugin_id,
    b.chat_id,
    b.sent_message_id,
    b.sent_status,
    b.attempts,
    b.next_attempt_at,
    c.status AS chat_status,
    c.title AS chat_title,
    c.owner_id AS chat_owner_id
FROM plugin_chat_bindings b
JOIN chats c ON c.id = b.chat_id
WHERE b.plugin_id = @plugin_id
    AND b.next_attempt_at <= now()
    AND c.status IN ('waiting', 'error', 'paused')
    AND NOT c.archived
    AND (
        EXISTS (
            SELECT 1
            FROM chat_messages m
            WHERE m.chat_id = b.chat_id
                AND m.id > b.sent_message_id
                AND m.role = 'assistant'
                AND m.visibility IN ('user', 'both')
                AND NOT m.deleted
        )
        OR c.status IS DISTINCT FROM b.sent_status
    )
ORDER BY b.next_attempt_at
LIMIT sqlc.arg('limit_count')::int;

-- name: UpdatePluginChatBindingDelivered :exec
UPDATE plugin_chat_bindings
SET
    sent_message_id = @sent_message_id,
    sent_status = @sent_status,
    attempts = 0,
    next_attempt_at = now()
WHERE plugin_id = @plugin_id AND chat_id = @chat_id;

-- name: UpdatePluginChatBindingFailed :exec
UPDATE plugin_chat_bindings
SET
    attempts = attempts + 1,
    next_attempt_at = @next_attempt_at
WHERE plugin_id = @plugin_id AND chat_id = @chat_id;
