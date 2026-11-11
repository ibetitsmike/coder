package coderd

// This file is fork code: it serves the agent type catalog of the
// fork-agent-definitions experiment.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/agentselect"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary List agent types
// @ID list-agent-types
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param project query string false "Project ID" format(uuid)
// @Param workspace query string false "Workspace ID" format(uuid)
// @Param model_config_id query string false "Model config ID of the new root chat" format(uuid)
// @Success 200 {array} codersdk.ForkAgentType
// @Router /api/v2/chats/fork/organizations/{organization}/agent-types [get]
// @x-apidocgen {"skip": true}
func (api *API) forkAgentTypes(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	if !api.requireChatDaemon(ctx, rw) {
		return
	}
	parser := httpapi.NewQueryParamParser()
	projectID := parser.UUID(r.URL.Query(), uuid.Nil, "project")
	workspaceID := parser.UUID(r.URL.Query(), uuid.Nil, "workspace")
	modelConfigID := parser.UUID(r.URL.Query(), uuid.Nil, "model_config_id")
	if len(parser.Errors) > 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message:     "Query parameters have invalid values.",
			Validations: parser.Errors,
		})
		return
	}
	opts := chatd.AgentTypeCatalogOptions{
		OwnerID:        httpmw.APIKey(r).UserID,
		OrganizationID: organization.ID,
	}
	if projectID != uuid.Nil {
		project, status, resp := api.validateChatProjectSelection(ctx, projectID, opts.OwnerID, organization.ID)
		if resp != nil {
			httpapi.Write(ctx, rw, status, *resp)
			return
		}
		opts.ProjectID = project
	}
	// The root model decides whether a definition's model resolves, so pick
	// it as chat creation does.
	createReq := codersdk.CreateChatRequest{OrganizationID: organization.ID}
	if modelConfigID != uuid.Nil {
		createReq.ModelConfigID = &modelConfigID
	}
	rootModelID, _, status, resp := api.resolveCreateChatModelConfigID(ctx, opts.OwnerID, createReq)
	// Chat creation lets a definition's model replace a missing default
	// model, so only the types without one become unavailable.
	if resp != nil && resp.Message == noLocalChatModelResponse().Message {
		opts.NoModelReason = resp.Message
	} else if resp != nil {
		httpapi.Write(ctx, rw, status, *resp)
		return
	}
	opts.ModelConfigID, opts.ExplicitModel = rootModelID, createReq.ModelConfigID != nil
	if workspaceID != uuid.Nil {
		// Workspace definitions come from the workspace agent, so the
		// caller needs the access that attaching a chat to it needs.
		_, workspace, status, resp := api.validateChatWorkspaceSelection(ctx, &workspaceID)
		if resp != nil {
			httpapi.Write(ctx, rw, status, *resp)
			return
		}
		if workspace.OrganizationID != organization.ID {
			httpapi.ResourceNotFound(rw)
			return
		}
		agents, err := api.Database.GetWorkspaceAgentsInLatestBuildByWorkspaceID(ctx, workspace.ID)
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		// A workspace without a chat agent reports no agent definitions.
		agent, err := agentselect.FindChatAgent(agents)
		switch {
		case err == nil:
			opts.WorkspaceAgentID = uuid.NullUUID{UUID: agent.ID, Valid: true}
		case !errors.Is(err, agentselect.ErrNoEligibleAgents):
			httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
				Message: chatWorkspaceUploadNoChatAgentMessage,
				Detail:  err.Error(),
			})
			return
		}
	}
	types, err := api.chatDaemon.AgentTypeCatalog(ctx, opts)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, types)
}
