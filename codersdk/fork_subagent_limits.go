package codersdk

// This file is fork code: it holds the organization subagent limits of the
// fork-nested-subagents experiment, so upstream's chats.go stays untouched.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// ForkOrganizationSubagentLimits are the subagent limits of an organization.
// A nil limit uses the deployment value, and a set limit can only lower it.
// MaxLivePerFamily is stored but not enforced yet.
type ForkOrganizationSubagentLimits struct {
	// MaxDepth is the maximum depth of nested subagent chats, between 1 and
	// 16.
	MaxDepth *int32 `json:"max_depth" example:"2"`
	// MaxConcurrent is the maximum number of running subagent chats in the
	// organization.
	MaxConcurrent *int32 `json:"max_concurrent" example:"8"`
	// MaxConcurrentPerFamily is the maximum number of running subagent chats
	// under one root chat.
	MaxConcurrentPerFamily *int32 `json:"max_concurrent_per_family" example:"4"`
	// MaxLivePerFamily is the maximum number of unarchived subagent chats
	// under one root chat.
	MaxLivePerFamily *int32 `json:"max_live_per_family" example:"6"`
}

// ForkOrganizationSubagentLimits returns the subagent limits of an
// organization.
func (c *Client) ForkOrganizationSubagentLimits(ctx context.Context, organizationID uuid.UUID) (ForkOrganizationSubagentLimits, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/subagent-limits", organizationID)
	return requestJSON[ForkOrganizationSubagentLimits](ctx, c, http.MethodGet, path, http.StatusOK, nil)
}

// UpdateForkOrganizationSubagentLimits replaces the subagent limits of an
// organization and returns the stored limits.
func (c *Client) UpdateForkOrganizationSubagentLimits(ctx context.Context, organizationID uuid.UUID, req ForkOrganizationSubagentLimits) (ForkOrganizationSubagentLimits, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/subagent-limits", organizationID)
	return requestJSON[ForkOrganizationSubagentLimits](ctx, c, http.MethodPut, path, http.StatusOK, req)
}
