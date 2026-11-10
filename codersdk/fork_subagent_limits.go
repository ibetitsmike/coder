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
// A nil limit, which is omitted from JSON, uses the deployment value, and a
// set limit can only lower it.
type ForkOrganizationSubagentLimits struct {
	// MaxDepth is the maximum depth of nested subagent chats, between 1 and
	// 16.
	MaxDepth *int32 `json:"max_depth,omitempty" example:"2"`
	// MaxConcurrent is the maximum number of running subagent chats in the
	// organization, not counting a subagent's own ancestors.
	MaxConcurrent *int32 `json:"max_concurrent,omitempty" example:"8"`
	// MaxConcurrentPerFamily is the maximum number of running subagent chats
	// under one root chat, not counting a subagent's own ancestors.
	MaxConcurrentPerFamily *int32 `json:"max_concurrent_per_family,omitempty" example:"4"`
	// MaxLivePerFamily is the maximum number of live subagent chats under one
	// root chat, checked when a subagent is spawned or when message_agent
	// resumes a finished one. A live subagent has status running,
	// interrupting, requires_action or paused and is not archived. A finished
	// one is waiting or error and does not count.
	MaxLivePerFamily *int32 `json:"max_live_per_family,omitempty" example:"6"`
}

// ForkSubagentLimitValues are subagent limits in force. A capacity limit of
// 0 means no limit, and the depth is always set.
type ForkSubagentLimitValues struct {
	// MaxDepth is the maximum depth of nested subagent chats.
	MaxDepth int32 `json:"max_depth" example:"2"`
	// MaxConcurrent is the maximum number of running subagent chats.
	MaxConcurrent int32 `json:"max_concurrent" example:"8"`
	// MaxConcurrentPerFamily is the maximum number of running subagent chats
	// under one root chat.
	MaxConcurrentPerFamily int32 `json:"max_concurrent_per_family" example:"4"`
	// MaxLivePerFamily is the maximum number of live subagent chats under one
	// root chat.
	MaxLivePerFamily int32 `json:"max_live_per_family" example:"6"`
}

// ForkOrganizationSubagentLimitsResponse holds the subagent limits of an
// organization with the deployment limits and the limits in force, where the
// lower of the deployment and organization values wins.
type ForkOrganizationSubagentLimitsResponse struct {
	// Organization holds the stored organization limits. A nil limit is
	// unset.
	Organization ForkOrganizationSubagentLimits `json:"organization"`
	// Deployment holds the deployment limits.
	Deployment ForkSubagentLimitValues `json:"deployment"`
	// Effective holds the limits in force in the organization.
	Effective ForkSubagentLimitValues `json:"effective"`
}

// ForkOrganizationSubagentLimits returns the subagent limits of an
// organization with the deployment and effective limits.
func (c *Client) ForkOrganizationSubagentLimits(ctx context.Context, organizationID uuid.UUID) (ForkOrganizationSubagentLimitsResponse, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/subagent-limits", organizationID)
	return requestJSON[ForkOrganizationSubagentLimitsResponse](ctx, c, http.MethodGet, path, http.StatusOK, nil)
}

// UpdateForkOrganizationSubagentLimits replaces the subagent limits of an
// organization and returns the stored limits with the deployment and
// effective limits.
func (c *Client) UpdateForkOrganizationSubagentLimits(ctx context.Context, organizationID uuid.UUID, req ForkOrganizationSubagentLimits) (ForkOrganizationSubagentLimitsResponse, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/subagent-limits", organizationID)
	return requestJSON[ForkOrganizationSubagentLimitsResponse](ctx, c, http.MethodPut, path, http.StatusOK, req)
}
