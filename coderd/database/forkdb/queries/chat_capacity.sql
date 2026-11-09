-- name: CountActiveSubagentsForCapacity :one
-- Counts the subagent chats that hold a capacity slot: owned by a worker with
-- a fresh heartbeat, like CountChatCapacityActiveByPool. The candidate and its
-- ancestors never count: a coordinator blocked in wait_agent holds a slot, so
-- its own descendants must never wait for it. The depth guard stops the walk
-- if a cycle ever appears in the hierarchy.
WITH RECURSIVE lineage(id, parent_chat_id, depth) AS (
	SELECT chats.id, chats.parent_chat_id, 0
	FROM chats
	WHERE chats.id = @chat_id::uuid
	UNION ALL
	SELECT chats.id, chats.parent_chat_id, lineage.depth + 1
	FROM chats
	JOIN lineage ON chats.id = lineage.parent_chat_id
	WHERE lineage.depth < 64
)
SELECT
	COUNT(*)::bigint AS active_count,
	(COUNT(*) FILTER (WHERE c.organization_id = @organization_id::uuid))::bigint AS active_organization_count,
	(COUNT(*) FILTER (WHERE c.root_chat_id = @root_chat_id::uuid))::bigint AS active_family_count
FROM chat_heartbeats hb
JOIN chats c
	ON c.id = hb.chat_id
	AND c.runner_id = hb.runner_id
WHERE c.worker_id IS NOT NULL
	AND c.parent_chat_id IS NOT NULL
	AND c.id NOT IN (SELECT lineage.id FROM lineage)
	AND hb.heartbeat_at > NOW() - (INTERVAL '1 second' * @stale_seconds::int);

-- name: CountLiveSubagentsInFamily :one
-- Counts the unarchived subagent chats under @root_chat_id that have not
-- finished. A finished subagent is waiting or error.
SELECT COUNT(*)::bigint AS live_count
FROM chats
WHERE chats.root_chat_id = @root_chat_id::uuid
	AND chats.archived = false
	AND chats.status IN ('running'::chat_status, 'interrupting'::chat_status, 'requires_action'::chat_status, 'paused'::chat_status);
