-- ListChatHistoryRows lists the rows that shape the context windows of a
-- chat: the compressed boundary rows, with their content, and the messages
-- that both the user and the model see, with an empty boundary_content.
-- Calls and results of the chat_history and read_chat_transcript tools are
-- left out, because the history tool skips them.
-- name: ListChatHistoryRows :many
SELECT
    id,
    created_at,
    role::text AS role,
    visibility::text AS visibility,
    compressed,
    COALESCE(CASE WHEN compressed THEN content END, '[]'::jsonb)::jsonb AS boundary_content
FROM chat_messages
WHERE chat_id = @chat_id::uuid
    AND deleted = false
    AND (
        compressed
        OR (
            visibility = 'both'
            AND NOT COALESCE(content @> '[{"tool_name": "chat_history"}]'::jsonb, false)
            AND NOT COALESCE(content @> '[{"tool_name": "read_chat_transcript"}]'::jsonb, false)
        )
    )
ORDER BY id;

-- SearchChatHistory returns the newest messages between after_id and
-- before_id, with before_id 0 for no upper bound, whose content holds
-- needle. The caller lowercases needle and escapes it as JSON string text,
-- since the match runs on the stored JSON.
-- name: SearchChatHistory :many
SELECT
    id,
    created_at,
    role::text AS role,
    content,
    content_version
FROM chat_messages
WHERE chat_id = @chat_id::uuid
    AND id > @after_id::bigint
    AND (@before_id::bigint = 0 OR id < @before_id::bigint)
    AND deleted = false
    AND compressed = false
    AND visibility = 'both'
    AND NOT COALESCE(content @> '[{"tool_name": "chat_history"}]'::jsonb, false)
    AND NOT COALESCE(content @> '[{"tool_name": "read_chat_transcript"}]'::jsonb, false)
    AND strpos(lower(content::text), @needle::text) > 0
ORDER BY id DESC
LIMIT @limit_count::int;
