-- requires: 01a1ca6a-c967-7052-ab8d-4298f2746e05_dedup_plugin_link_active_chats

-- The previous release sent each chat to every link of its owner and kept
-- no record of it, so every existing binding counts as delivered. A default
-- of true marks them without rewriting the table; new bindings start false.
ALTER TABLE plugin_chat_bindings ADD COLUMN delivered boolean NOT NULL DEFAULT true;
ALTER TABLE plugin_chat_bindings ALTER COLUMN delivered SET DEFAULT false;

COMMENT ON COLUMN plugin_chat_bindings.delivered IS 'Whether an event of the chat reached a link. Delivery gives a chat that no link follows to an idle link only while this is false.';
