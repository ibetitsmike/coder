-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1bc7b-6e0c-783d-8945-1c634ab35eea_plugins

-- Delivery sends a chat's events only to the link whose active chat it is,
-- so at most one link of a plugin may follow each chat. The previous
-- implementation pointed every link of a user at the chat it last
-- delivered, and nothing records which identity opened a shared chat, so
-- clear every link of an ambiguous group. A cleared link re-points at the
-- next chat started from it.
UPDATE plugin_user_links
SET active_chat_id = NULL
WHERE active_chat_id IS NOT NULL
    AND (plugin_id, active_chat_id) IN (
        SELECT plugin_id, active_chat_id
        FROM plugin_user_links
        WHERE active_chat_id IS NOT NULL
        GROUP BY plugin_id, active_chat_id
        HAVING count(*) > 1
    );

-- The index also stops a replica still running the previous release from
-- recreating shared pointers during a rolling upgrade.
CREATE UNIQUE INDEX plugin_user_links_active_chat_idx
    ON plugin_user_links (plugin_id, active_chat_id)
    WHERE active_chat_id IS NOT NULL;
