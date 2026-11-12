-- name: GetChatOrganizationWakeupCaps :one
SELECT * FROM chat_organization_wakeup_caps WHERE organization_id = @organization_id::uuid;

-- name: UpsertChatOrganizationWakeupCaps :one
INSERT INTO chat_organization_wakeup_caps (organization_id, max_chat_runs_per_day, max_organization_runs_per_day, max_chat_spend_micros_per_day, max_organization_spend_micros_per_day)
VALUES (@organization_id, sqlc.narg('max_chat_runs_per_day')::integer, sqlc.narg('max_organization_runs_per_day')::integer, sqlc.narg('max_chat_spend_micros_per_day')::bigint, sqlc.narg('max_organization_spend_micros_per_day')::bigint)
ON CONFLICT (organization_id) DO UPDATE SET
    max_chat_runs_per_day = EXCLUDED.max_chat_runs_per_day,
    max_organization_runs_per_day = EXCLUDED.max_organization_runs_per_day,
    max_chat_spend_micros_per_day = EXCLUDED.max_chat_spend_micros_per_day,
    max_organization_spend_micros_per_day = EXCLUDED.max_organization_spend_micros_per_day,
    updated_at = now()
RETURNING *;
