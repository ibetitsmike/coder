-- name: CountLiveSubagentsInFamily :one
-- Counts the unarchived subagent chats under @root_chat_id that have not
-- finished. A finished subagent is waiting or error. A workflow step child
-- whose step is reported counts as finished: the workflow is done with it
-- and admits its next step while the child ends its turn. A user message
-- after the report, such as a message_agent that resumes the child, starts
-- new work, so the child counts again. A step's ID is its child chat ID.
SELECT COUNT(*)::bigint AS live_count
FROM chats
WHERE chats.root_chat_id = @root_chat_id::uuid
	AND chats.archived = false
	AND chats.status IN ('running'::chat_status, 'interrupting'::chat_status, 'requires_action'::chat_status, 'paused'::chat_status)
	AND NOT EXISTS (
		SELECT 1 FROM workflow_steps
		WHERE workflow_steps.id = chats.id
			AND workflow_steps.status = 'reported'
			AND NOT EXISTS (
				SELECT 1 FROM chat_messages
				WHERE chat_messages.chat_id = chats.id
					AND chat_messages.role = 'user'::chat_message_role
					AND chat_messages.deleted = false
					AND chat_messages.created_at > COALESCE(workflow_steps.finished_at, workflow_steps.started_at)
			)
	);

-- name: GetSubagentCapacityCounts :many
-- Returns, for each subagent chat in @candidate_ids or, when @root_chat_id is
-- set, in the family of @root_chat_id, the subagent chats that
-- hold a capacity slot: owned by a worker with a fresh heartbeat, like
-- CountChatCapacityActiveByPool. active_count spans the deployment,
-- active_organization_count the candidate's organization and
-- active_family_count the candidate's family. The candidate and its
-- ancestors never count: a coordinator blocked in wait_agent holds a slot, so
-- its own descendants must never wait for it. With @awaiting_only, only the
-- unarchived running candidates that no worker with a fresh heartbeat owns
-- get a row. The counts are the totals minus each candidate's own active
-- lineage, so one statement answers for every candidate without pairing each
-- candidate with every active subagent. The depth guard stops the walk if a
-- cycle ever appears in the hierarchy.
WITH RECURSIVE candidates AS (
	SELECT
		c.id,
		c.parent_chat_id,
		c.organization_id,
		COALESCE(c.root_chat_id, c.parent_chat_id) AS family_root_id
	FROM chats c
	WHERE (c.id = ANY(@candidate_ids::uuid[]) OR c.root_chat_id = sqlc.narg('root_chat_id')::uuid)
		AND c.parent_chat_id IS NOT NULL
		AND (
			NOT @awaiting_only::boolean
			OR (
				c.archived = false
				AND c.status = 'running'::chat_status
				AND (
					c.worker_id IS NULL
					OR c.runner_id IS NULL
					OR NOT EXISTS (
						SELECT 1
						FROM chat_heartbeats hb
						WHERE hb.chat_id = c.id
							AND hb.runner_id = c.runner_id
							AND hb.heartbeat_at > NOW() - (INTERVAL '1 second' * @stale_seconds::int)
					)
				)
			)
		)
),
lineage(candidate_id, id, parent_chat_id, depth) AS (
	SELECT candidates.id, candidates.id, candidates.parent_chat_id, 0
	FROM candidates
	UNION ALL
	SELECT lineage.candidate_id, chats.id, chats.parent_chat_id, lineage.depth + 1
	FROM chats
	JOIN lineage ON chats.id = lineage.parent_chat_id
	WHERE lineage.depth < 64
),
active AS (
	SELECT c.id, c.organization_id, c.root_chat_id
	FROM chat_heartbeats hb
	JOIN chats c
		ON c.id = hb.chat_id
		AND c.runner_id = hb.runner_id
	WHERE c.worker_id IS NOT NULL
		AND c.parent_chat_id IS NOT NULL
		AND hb.heartbeat_at > NOW() - (INTERVAL '1 second' * @stale_seconds::int)
),
organization_totals AS (
	SELECT active.organization_id, COUNT(*) AS active_count
	FROM active
	GROUP BY active.organization_id
),
family_totals AS (
	SELECT active.root_chat_id, COUNT(*) AS active_count
	FROM active
	GROUP BY active.root_chat_id
),
excluded AS (
	SELECT DISTINCT lineage.candidate_id, active.id, active.organization_id, active.root_chat_id
	FROM lineage
	JOIN active ON active.id = lineage.id
),
excluded_counts AS (
	SELECT
		excluded.candidate_id,
		COUNT(*) AS active_count,
		COUNT(*) FILTER (WHERE excluded.organization_id = candidates.organization_id) AS active_organization_count,
		COUNT(*) FILTER (WHERE excluded.root_chat_id = candidates.family_root_id) AS active_family_count
	FROM excluded
	JOIN candidates ON candidates.id = excluded.candidate_id
	GROUP BY excluded.candidate_id
)
SELECT
	candidates.id AS chat_id,
	((SELECT COUNT(*) FROM active) - COALESCE(excluded_counts.active_count, 0))::bigint AS active_count,
	(COALESCE(organization_totals.active_count, 0) - COALESCE(excluded_counts.active_organization_count, 0))::bigint AS active_organization_count,
	(COALESCE(family_totals.active_count, 0) - COALESCE(excluded_counts.active_family_count, 0))::bigint AS active_family_count
FROM candidates
LEFT JOIN organization_totals ON organization_totals.organization_id = candidates.organization_id
LEFT JOIN family_totals ON family_totals.root_chat_id = candidates.family_root_id
LEFT JOIN excluded_counts ON excluded_counts.candidate_id = candidates.id
ORDER BY candidates.id;

-- name: AcquireSharedLock :exec
-- Takes the advisory lock @lock_id in shared mode until the transaction
-- ends. It blocks while another transaction holds the lock with AcquireLock,
-- and AcquireLock blocks while it is held, but shared holders do not block
-- each other.
SELECT pg_advisory_xact_lock_shared(@lock_id::bigint);
