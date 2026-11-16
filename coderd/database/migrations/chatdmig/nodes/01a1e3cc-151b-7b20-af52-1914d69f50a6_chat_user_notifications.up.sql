-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1e197-0d6a-7ab6-a3a5-c8a54aee0336_chat_wakeup_run_counts

-- One row per notify_user call that passed the hourly limit of its chat, and
-- one per wake-up pause notification sent about it. The limit counts these
-- rows, so it never reads notification_messages. dbpurge deletes the rows
-- older than a day. The notifications sent before this node are not counted,
-- so each chat may send a full hour of them again once.
CREATE TABLE chat_user_notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL
);
CREATE INDEX chat_user_notifications_chat_id_created_at_idx ON chat_user_notifications (chat_id, created_at);
