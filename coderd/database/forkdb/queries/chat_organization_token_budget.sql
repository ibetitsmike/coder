-- name: GetChatOrganizationTokenBudget :one
SELECT * FROM chat_organization_token_budget WHERE organization_id = @organization_id::uuid;

-- name: UpsertChatOrganizationTokenBudget :one
INSERT INTO chat_organization_token_budget (organization_id, strategy, fallback)
VALUES (@organization_id, @strategy::text, @fallback::text)
ON CONFLICT (organization_id) DO UPDATE SET
    strategy = EXCLUDED.strategy,
    fallback = EXCLUDED.fallback,
    updated_at = now()
RETURNING *;
