-- requires: 01a1ca6a-c967-7052-ab8d-4298f2746e05_dedup_plugin_link_active_chats

ALTER TABLE plugin_chat_bindings ADD COLUMN delivered boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN plugin_chat_bindings.delivered IS 'Whether an event of the chat reached a link. Delivery gives a chat that no link follows to an idle link only while this is false.';

-- The previous release sent each chat to every link of its owner and kept
-- no record of it. A recorded status means the chat may have gone out.
UPDATE plugin_chat_bindings SET delivered = true WHERE sent_status IS NOT NULL;
