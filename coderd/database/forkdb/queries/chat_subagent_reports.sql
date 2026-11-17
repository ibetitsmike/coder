-- name: GetChatReportMessages :many
-- Returns the newest assistant messages of a chat that the user sees, below
-- @before_id (0 for no bound), newest first. These are the rows upstream's
-- latestSubagentAssistantMessage scans for a subagent's report, read a page
-- at a time instead of the whole history.
SELECT
    id,
    content,
    content_version
FROM chat_messages
WHERE chat_id = @chat_id::uuid
    AND (@before_id::bigint = 0 OR id < @before_id::bigint)
    AND role = 'assistant'
    AND visibility IN ('user', 'both')
    AND deleted = false
ORDER BY id DESC
LIMIT @limit_count::int;
