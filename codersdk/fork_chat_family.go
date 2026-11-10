package codersdk

// This file is fork code: it holds the chat family capacity of the
// fork-nested-subagents experiment, so upstream's chats.go stays untouched.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// ForkChatFamilyCapacity is the subagent capacity of the family under a root
// chat. It is computed without a lock, so it may be stale when read.
type ForkChatFamilyCapacity struct {
	// LiveSubagents counts the unarchived subagents that have not finished:
	// status running, interrupting, requires_action or paused.
	LiveSubagents int64 `json:"live_subagents" example:"3"`
	// MaxLiveSubagents is the family's live subagent limit in force. 0 means
	// no limit. A spawn, or a message_agent that resumes a finished
	// subagent, fails while LiveSubagents is at the limit.
	MaxLiveSubagents int64 `json:"max_live_subagents" example:"6"`
	// WaitingForCapacity lists the running subagents that no worker owns
	// because a deployment, organization or family concurrency limit is
	// full.
	WaitingForCapacity []uuid.UUID `json:"waiting_for_capacity" format:"uuid"`
}

// ForkChatFamilyCapacity returns the subagent capacity of the family under
// the root chat chatID.
func (c *Client) ForkChatFamilyCapacity(ctx context.Context, chatID uuid.UUID) (ForkChatFamilyCapacity, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/%s/family", chatID)
	return requestJSON[ForkChatFamilyCapacity](ctx, c, http.MethodGet, path, http.StatusOK, nil)
}
