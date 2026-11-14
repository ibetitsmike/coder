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
-- table check keeps the size cap. With call_id set it records the call and
-- its result in chat_note_calls, and changes nothing when the call is
-- recorded already, so a replayed call does not append twice. Two runs of
-- the same call that start together both change the note, and the second
-- then fails on the record's primary key and rolls back. It also drops the
-- chat's records older than a day.
-- name: WriteChatNote :one
WITH note AS (
    INSERT INTO chat_notes (chat_id, path, content)
    SELECT @chat_id::uuid, @path::text, @content::text
    WHERE (
            EXISTS (SELECT 1 FROM chat_notes WHERE chat_id = @chat_id::uuid AND path = @path::text)
            OR (SELECT COUNT(*) FROM chat_notes WHERE chat_id = @chat_id::uuid) < @max_notes::bigint
        )
        AND NOT EXISTS (SELECT 1 FROM chat_note_calls WHERE id = sqlc.narg('call_id')::uuid)
    ON CONFLICT (chat_id, path) DO UPDATE
    SET
        content = CASE
            WHEN @append::boolean AND chat_notes.content <> '' THEN chat_notes.content || E'\n' || EXCLUDED.content
            ELSE EXCLUDED.content
        END,
        updated_at = now()
    RETURNING *
), recorded AS (
    INSERT INTO chat_note_calls (id, chat_id, path, bytes, updated_at)
    SELECT sqlc.narg('call_id')::uuid, note.chat_id, note.path, octet_length(note.content), note.updated_at
    FROM note
    WHERE sqlc.narg('call_id')::uuid IS NOT NULL
), expired AS (
    DELETE FROM chat_note_calls
    WHERE chat_id = @chat_id::uuid AND created_at < now() - interval '1 day'
)
SELECT note.chat_id, note.path, note.content, note.created_at, note.updated_at FROM note;

-- DeleteChatNote deletes a note and returns the number of deleted notes.
-- With call_id set it records the call like WriteChatNote, and deletes
-- nothing when the call is recorded already, so a replayed delete does not
-- delete a note that a later call wrote.
-- name: DeleteChatNote :one
WITH deleted AS (
    DELETE FROM chat_notes
    WHERE chat_id = @chat_id::uuid AND path = @path::text
        AND NOT EXISTS (SELECT 1 FROM chat_note_calls WHERE id = sqlc.narg('call_id')::uuid)
    RETURNING path
), recorded AS (
    INSERT INTO chat_note_calls (id, chat_id, path, deleted, updated_at)
    SELECT sqlc.narg('call_id')::uuid, @chat_id::uuid, @path::text, EXISTS (SELECT 1 FROM deleted), now()
    WHERE sqlc.narg('call_id')::uuid IS NOT NULL
        AND NOT EXISTS (SELECT 1 FROM chat_note_calls WHERE id = sqlc.narg('call_id')::uuid)
), expired AS (
    DELETE FROM chat_note_calls
    WHERE chat_id = @chat_id::uuid AND created_at < now() - interval '1 day'
)
SELECT COUNT(*)::bigint FROM deleted;

-- name: GetChatNoteCall :one
SELECT * FROM chat_note_calls WHERE id = @id::uuid AND chat_id = @chat_id::uuid;
