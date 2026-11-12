-- name: GetChatPinnedSkillsByChatID :many
SELECT * FROM chat_pinned_skills WHERE chat_id = @chat_id::uuid ORDER BY skill_source, skill_name;

-- name: DeleteChatPinnedSkills :exec
DELETE FROM chat_pinned_skills WHERE chat_id = @chat_id::uuid;

-- name: InsertChatPinnedSkills :exec
INSERT INTO chat_pinned_skills (chat_id, skill_source, skill_name)
SELECT
    @chat_id::uuid,
    unnest(@skill_sources::text[]),
    unnest(@skill_names::text[])
ON CONFLICT DO NOTHING;
