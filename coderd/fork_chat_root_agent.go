package coderd

// This file is fork code: it resolves the agent_type of a create chat request
// behind the fork-agent-definitions experiment. Upstream's createChat calls
// forkCreateChatRootAgent once, after it resolves the chat's model, and lets
// it report that model's failure.

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/httpapi/httperror"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
)

// forkCreateChatRootAgent resolves req.AgentType, and returns the agent with
// the chat's model and fallback reasoning effort. A definition's model
// replaces modelConfigID unless the request chose one, and drops the effort
// of the replaced model. A definition's effort replaces effort. The request's
// own reasoning_effort still wins over both. modelErr, with modelStatus, is
// the failure to resolve modelConfigID. A missing default model only fails
// the request when the definition sets no model either.
func (api *API) forkCreateChatRootAgent(
	ctx context.Context,
	ownerID uuid.UUID,
	projectID uuid.NullUUID,
	req codersdk.CreateChatRequest,
	modelConfigID uuid.UUID,
	effort *string,
	modelStatus int,
	modelErr *codersdk.Response,
) (*chatd.RootAgent, uuid.UUID, *string, error) {
	if modelErr != nil && (req.AgentType == "" || modelErr.Message != noLocalChatModelResponse().Message) {
		return nil, uuid.Nil, nil, httperror.NewResponseError(modelStatus, *modelErr)
	}
	if req.AgentType == "" {
		return nil, modelConfigID, effort, nil
	}
	if !api.Experiments.Enabled(codersdk.ExperimentForkAgentDefinitions) {
		return nil, uuid.Nil, nil, httperror.NewResponseError(http.StatusBadRequest, codersdk.Response{
			Message: "agent_type requires the fork-agent-definitions experiment.",
		})
	}
	agent, err := api.chatDaemon.ResolveRootAgent(ctx, chatd.RootAgentOptions{
		OwnerID:        ownerID,
		OrganizationID: req.OrganizationID,
		ProjectID:      projectID,
		AgentType:      req.AgentType,
		ModelConfigID:  modelConfigID,
		ExplicitModel:  req.ModelConfigID != nil,
	})
	if err != nil {
		var agentErr *chatd.RootAgentError
		if errors.As(err, &agentErr) {
			return nil, uuid.Nil, nil, httperror.NewResponseError(http.StatusBadRequest, codersdk.Response{
				Message: "Invalid agent_type.",
				Detail:  agentErr.Reason,
			})
		}
		return nil, uuid.Nil, nil, httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to resolve agent_type.",
			Detail:  err.Error(),
		})
	}
	if agent.ModelConfigID.Valid {
		modelConfigID = agent.ModelConfigID.UUID
		effort = nil
	}
	if modelConfigID == uuid.Nil {
		return nil, uuid.Nil, nil, httperror.NewResponseError(modelStatus, *modelErr)
	}
	if agent.ReasoningEffort != "" {
		effort = &agent.ReasoningEffort
	}
	return &agent, modelConfigID, effort, nil
}
