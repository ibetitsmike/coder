package codersdk

// This file is fork code: it holds the pinned skill types and client methods
// of the fork-pinned-skills experiment.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// ChatPinnedSkill names a skill pinned to a chat. chatd puts its body in the
// prompt of every turn until it is unpinned, and skips a pinned skill that the
// chat owner cannot use, such as one that was deleted or disabled.
type ChatPinnedSkill struct {
	Source ChatSkillSource `json:"source"`
	Name   string          `json:"name"`
}

// UpdateChatPinnedSkillsRequest replaces every pinned skill of a chat. An
// empty list unpins them all.
type UpdateChatPinnedSkillsRequest struct {
	Skills []ChatPinnedSkill `json:"skills"`
}

func forkChatPinnedSkillsPath(chatID uuid.UUID) string {
	return fmt.Sprintf("/api/v2/chats/fork/%s/pinned-skills", chatID)
}

// ChatPinnedSkills returns the skills pinned to a chat.
func (c *Client) ChatPinnedSkills(ctx context.Context, chatID uuid.UUID) ([]ChatPinnedSkill, error) {
	return requestJSON[[]ChatPinnedSkill](ctx, c, http.MethodGet, forkChatPinnedSkillsPath(chatID), http.StatusOK, nil)
}

// UpdateChatPinnedSkills replaces the skills pinned to a chat and returns the
// stored list.
func (c *Client) UpdateChatPinnedSkills(ctx context.Context, chatID uuid.UUID, req UpdateChatPinnedSkillsRequest) ([]ChatPinnedSkill, error) {
	return requestJSON[[]ChatPinnedSkill](ctx, c, http.MethodPut, forkChatPinnedSkillsPath(chatID), http.StatusOK, req)
}
