package codersdk

// This file is fork code: it holds the organization wake-up caps of the
// fork-chat-wakeups experiment, so upstream's chats.go stays untouched.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// ForkOrganizationWakeupCaps are the daily wake-up caps of an organization,
// over the rolling 24 hours before each wake-up. A nil cap, which is omitted
// from JSON, uses the built-in default. A set cap replaces the default and can
// be higher or lower.
type ForkOrganizationWakeupCaps struct {
	// MaxChatRunsPerDay is the maximum number of wake-up runs of one chat.
	MaxChatRunsPerDay *int32 `json:"max_chat_runs_per_day,omitempty" example:"288"`
	// MaxOrganizationRunsPerDay is the maximum number of wake-up runs of all
	// chats in the organization.
	MaxOrganizationRunsPerDay *int32 `json:"max_organization_runs_per_day,omitempty" example:"2880"`
	// MaxChatSpendMicrosPerDay is the maximum model spend of one chat with a
	// wake-up, in millionths of a dollar.
	MaxChatSpendMicrosPerDay *int64 `json:"max_chat_spend_micros_per_day,omitempty" example:"20000000"`
	// MaxOrganizationSpendMicrosPerDay is the maximum model spend of all
	// chats with a wake-up in the organization, in millionths of a dollar.
	MaxOrganizationSpendMicrosPerDay *int64 `json:"max_organization_spend_micros_per_day,omitempty" example:"200000000"`
}

// ForkWakeupCapValues are wake-up caps in force.
type ForkWakeupCapValues struct {
	MaxChatRunsPerDay                int64 `json:"max_chat_runs_per_day" example:"288"`
	MaxOrganizationRunsPerDay        int64 `json:"max_organization_runs_per_day" example:"2880"`
	MaxChatSpendMicrosPerDay         int64 `json:"max_chat_spend_micros_per_day" example:"20000000"`
	MaxOrganizationSpendMicrosPerDay int64 `json:"max_organization_spend_micros_per_day" example:"200000000"`
}

// ForkOrganizationWakeupCapsResponse holds the wake-up caps of an
// organization with the defaults and the caps in force.
type ForkOrganizationWakeupCapsResponse struct {
	// Organization holds the stored organization caps. A nil cap is unset.
	Organization ForkOrganizationWakeupCaps `json:"organization"`
	// Default holds the built-in caps.
	Default ForkWakeupCapValues `json:"default"`
	// Effective holds the caps in force in the organization.
	Effective ForkWakeupCapValues `json:"effective"`
}

// ForkOrganizationWakeupCaps returns the wake-up caps of an organization with
// the default and effective caps.
func (c *Client) ForkOrganizationWakeupCaps(ctx context.Context, organizationID uuid.UUID) (ForkOrganizationWakeupCapsResponse, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/wakeup-caps", organizationID)
	return requestJSON[ForkOrganizationWakeupCapsResponse](ctx, c, http.MethodGet, path, http.StatusOK, nil)
}

// UpdateForkOrganizationWakeupCaps replaces the wake-up caps of an
// organization and returns the stored caps with the default and effective
// caps.
func (c *Client) UpdateForkOrganizationWakeupCaps(ctx context.Context, organizationID uuid.UUID, req ForkOrganizationWakeupCaps) (ForkOrganizationWakeupCapsResponse, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/wakeup-caps", organizationID)
	return requestJSON[ForkOrganizationWakeupCapsResponse](ctx, c, http.MethodPut, path, http.StatusOK, req)
}
