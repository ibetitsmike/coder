-- requires: 01a1d71e-84b7-78ae-99dd-2cb2e94742e3_chat_notes

CREATE TABLE chat_note_calls (
    -- The tool call ID of the notes call that wrote, appended to or deleted
    -- the note.
    id uuid PRIMARY KEY,
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    path text NOT NULL,
    -- The result of the call: the size and time of the note it wrote, or for
    -- a delete whether a note was deleted.
    bytes bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL,
    deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX chat_note_calls_chat_id_created_at_idx ON chat_note_calls (chat_id, created_at);

COMMENT ON TABLE chat_note_calls IS 'Notes calls that changed a note, so that a replayed call returns its result without changing the note again.';
