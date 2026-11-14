-- name: ListChatNotes :many
SELECT
    path,
    octet_length(content)::bigint AS bytes,
    updated_at
FROM chat_notes
WHERE chat_id = @chat_id::uuid
ORDER BY path;

-- name: GetChatNote :one
SELECT * FROM chat_notes WHERE chat_id = @chat_id::uuid AND path = @path::text;

-- WriteChatNote replaces a note, or with append set adds content as a new
-- line at its end. It creates a missing note only while the chat has fewer
-- than max_notes notes, and returns no row when the chat is at the limit. The
-- table check keeps the size cap.
-- name: WriteChatNote :one
INSERT INTO chat_notes (chat_id, path, content)
SELECT @chat_id::uuid, @path::text, @content::text
WHERE EXISTS (SELECT 1 FROM chat_notes WHERE chat_id = @chat_id::uuid AND path = @path::text)
    OR (SELECT COUNT(*) FROM chat_notes WHERE chat_id = @chat_id::uuid) < @max_notes::bigint
ON CONFLICT (chat_id, path) DO UPDATE
SET
    content = CASE
        WHEN @append::boolean AND chat_notes.content <> '' THEN chat_notes.content || E'\n' || EXCLUDED.content
        ELSE EXCLUDED.content
    END,
    updated_at = now()
RETURNING *;

-- name: DeleteChatNote :execrows
DELETE FROM chat_notes WHERE chat_id = @chat_id::uuid AND path = @path::text;
