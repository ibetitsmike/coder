INSERT INTO plugins (
    id,
    organization_id,
    name,
    version,
    file_id,
    manifest,
    grants,
    created_by
)
SELECT
    'f6400000-0000-4000-8000-000000000001',
    c.organization_id,
    'fixture-plugin',
    '1.0.0',
    (SELECT id FROM files ORDER BY created_at LIMIT 1),
    '{}',
    '{}',
    c.owner_id
FROM chats c
ORDER BY c.created_at, c.id
LIMIT 1;

INSERT INTO plugin_user_links (plugin_id, external_id, user_id, active_chat_id)
SELECT p.id, '+15550100', p.created_by, c.id
FROM plugins p
JOIN chats c ON c.owner_id = p.created_by AND c.organization_id = p.organization_id
WHERE p.id = 'f6400000-0000-4000-8000-000000000001'
ORDER BY c.created_at, c.id
LIMIT 1;

INSERT INTO plugin_link_codes (code_hash, plugin_id, user_id, expires_at)
SELECT '\x00'::bytea, p.id, p.created_by, '2099-01-01 00:00:00+00'
FROM plugins p
WHERE p.id = 'f6400000-0000-4000-8000-000000000001';

INSERT INTO plugin_chat_bindings (plugin_id, chat_id, sent_message_id, sent_status)
SELECT l.plugin_id, l.active_chat_id, 0, 'waiting'
FROM plugin_user_links l
WHERE l.plugin_id = 'f6400000-0000-4000-8000-000000000001';
