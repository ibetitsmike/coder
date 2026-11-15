-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: 01a1df59-b98e-720a-869b-586a3dcd3799_chat_wakeup_watches_backfill

-- The open watches of active wake-ups that have something to deliver to an
-- idle chat: an event that someone observed, or a child agent that is done.
-- A child is done when it is archived, or when it settled in a status other
-- than running, interrupting or paused and waits neither for events of its
-- own (an open watch on an active wake-up) nor for an answer to its
-- question. A deleted child deletes its watch, so it never shows up here.
-- ListReadyChatWakeupWatches reads it, and ListDueChatWakeups makes an events
-- wake-up due when it has a child_done row here whose last send did not fail.
CREATE VIEW chat_wakeup_ready_watches AS
SELECT x.id, x.wakeup_id, x.chat_id, x.kind, x.child_chat_id, x.failed_at
FROM chat_wakeup_watches x
JOIN chat_wakeups w ON w.id = x.wakeup_id AND w.status = 'active'
JOIN chats c ON c.id = x.chat_id
LEFT JOIN chats k ON k.id = x.child_chat_id
WHERE x.delivered_at IS NULL
    AND NOT c.archived
    AND c.status NOT IN ('running', 'interrupting', 'requires_action', 'paused')
    AND NOT (c.status = 'error' AND EXISTS (SELECT 1 FROM chat_queued_messages qm WHERE qm.chat_id = c.id))
    AND (
        x.fired_at IS NOT NULL
        OR (x.kind = 'child_done' AND (
            k.archived
            OR (k.status NOT IN ('running', 'interrupting', 'paused')
                AND NOT EXISTS (
                    SELECT 1
                    FROM chat_wakeup_watches y
                    JOIN chat_wakeups yw ON yw.id = y.wakeup_id AND yw.status = 'active'
                    WHERE y.chat_id = k.id
                        AND y.delivered_at IS NULL
                )
                AND NOT EXISTS (
                    SELECT 1
                    FROM chat_descendant_questions q
                    WHERE q.chat_id = k.id
                        AND q.answered_at IS NULL
                ))
        ))
    );

-- The waker delivers a wake-up in the transaction that locks its row, so no
-- worker claims a row for a lease any more. Dropping the column changes no
-- wake-up: each one keeps its status and its next run time.
ALTER TABLE chat_wakeups DROP COLUMN lease_expires_at;
