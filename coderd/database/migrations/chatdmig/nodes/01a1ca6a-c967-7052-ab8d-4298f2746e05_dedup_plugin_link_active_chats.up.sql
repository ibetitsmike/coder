-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1bc7b-6e0c-783d-8945-1c634ab35eea_plugins

-- The previous implementation moved the active chat of every link of a
-- user whenever it delivered one of their chats, so upgrading leaves
-- several links of one user pointing at the same chat. Delivery now
-- selects links by active_chat_id equality, and that only reaches a
-- single identity if at most one link follows each chat. Keep the first
-- link by external_id on each chat, the identity the old delivery loop
-- reached first, and clear the rest; a cleared link re-points at the
-- next chat started from it.
WITH duplicates AS (
    SELECT plugin_id,
           external_id,
           ROW_NUMBER() OVER (
               PARTITION BY plugin_id, user_id, active_chat_id
               ORDER BY external_id
           ) AS rn
    FROM plugin_user_links
    WHERE active_chat_id IS NOT NULL
)
UPDATE plugin_user_links
SET active_chat_id = NULL
FROM duplicates
WHERE plugin_user_links.plugin_id = duplicates.plugin_id
    AND plugin_user_links.external_id = duplicates.external_id
    AND duplicates.rn > 1;

