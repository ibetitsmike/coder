package codersdk

// This file is fork code: it holds the organization compaction setting of the
// fork-token-budget experiment, so upstream's chats.go stays untouched.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// ForkTokenBudgetStrategy is how the chats of an organization compact their
// context when the fork-token-budget experiment is on.
type ForkTokenBudgetStrategy string

const (
	// ForkTokenBudgetStrategySummary summarizes the conversation, as chats do
	// without the experiment.
	ForkTokenBudgetStrategySummary ForkTokenBudgetStrategy = "summary"
	// ForkTokenBudgetStrategyTokenBudget works in context windows: the model
	// gets a handoff and a final request, saves a checkpoint in its notes and
	// calls new_context.
	ForkTokenBudgetStrategyTokenBudget ForkTokenBudgetStrategy = "token_budget"
)

// ForkTokenBudgetStrategies lists the valid strategies.
var ForkTokenBudgetStrategies = []ForkTokenBudgetStrategy{
	ForkTokenBudgetStrategySummary,
	ForkTokenBudgetStrategyTokenBudget,
}

// ForkTokenBudgetFallback is what a token-budget chat does when it reaches
// the usable limit of its context window without calling new_context.
type ForkTokenBudgetFallback string

const (
	// ForkTokenBudgetFallbackNewWindow starts the next context window from
	// the notes of the chat, with no summary.
	ForkTokenBudgetFallbackNewWindow ForkTokenBudgetFallback = "new_window"
	// ForkTokenBudgetFallbackSummary summarizes the conversation.
	ForkTokenBudgetFallbackSummary ForkTokenBudgetFallback = "summary"
)

// ForkTokenBudgetFallbacks lists the valid fallbacks.
var ForkTokenBudgetFallbacks = []ForkTokenBudgetFallback{
	ForkTokenBudgetFallbackNewWindow,
	ForkTokenBudgetFallbackSummary,
}

// ForkOrganizationTokenBudget is the compaction setting of an organization.
// An organization that never saved one uses the summary strategy and the
// new_window fallback.
type ForkOrganizationTokenBudget struct {
	// Strategy is how chats compact their context.
	Strategy ForkTokenBudgetStrategy `json:"strategy" enums:"summary,token_budget"`
	// Fallback is what a token-budget chat does when it reaches the usable
	// limit without calling new_context. It applies only with the
	// token_budget strategy.
	Fallback ForkTokenBudgetFallback `json:"fallback" enums:"new_window,summary"`
}

// DefaultForkOrganizationTokenBudget is the setting of an organization that
// never saved one.
func DefaultForkOrganizationTokenBudget() ForkOrganizationTokenBudget {
	return ForkOrganizationTokenBudget{
		Strategy: ForkTokenBudgetStrategySummary,
		Fallback: ForkTokenBudgetFallbackNewWindow,
	}
}

// ForkOrganizationTokenBudget returns the compaction setting of an
// organization.
func (c *Client) ForkOrganizationTokenBudget(ctx context.Context, organizationID uuid.UUID) (ForkOrganizationTokenBudget, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/token-budget", organizationID)
	return requestJSON[ForkOrganizationTokenBudget](ctx, c, http.MethodGet, path, http.StatusOK, nil)
}

// UpdateForkOrganizationTokenBudget replaces the compaction setting of an
// organization and returns the stored setting.
func (c *Client) UpdateForkOrganizationTokenBudget(ctx context.Context, organizationID uuid.UUID, req ForkOrganizationTokenBudget) (ForkOrganizationTokenBudget, error) {
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/token-budget", organizationID)
	return requestJSON[ForkOrganizationTokenBudget](ctx, c, http.MethodPut, path, http.StatusOK, req)
}
