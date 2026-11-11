package coderd

// This file is fork code: it serves the agent type catalog of the
// fork-agent-definitions experiment.

import (
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
		project, err := api.Database.GetChatProjectByID(ctx, projectID)
		if httpapi.Is404Error(err) || (err == nil && project.OrganizationID != organization.ID) {
			httpapi.ResourceNotFound(rw)
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		opts.ProjectID = uuid.NullUUID{UUID: projectID, Valid: true}
	}
	if workspaceID != uuid.Nil {
		workspace, err := api.Database.GetWorkspaceByID(ctx, workspaceID)
		if httpapi.Is404Error(err) || (err == nil && workspace.OrganizationID != organization.ID) {
			httpapi.ResourceNotFound(rw)
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		agents, err := api.Database.GetWorkspaceAgentsInLatestBuildByWorkspaceID(ctx, workspace.ID)
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		// A workspace without a chat agent reports no agent definitions.
		if agent, err := agentselect.FindChatAgent(agents); err == nil {
			opts.WorkspaceAgentID = uuid.NullUUID{UUID: agent.ID, Valid: true}
		}
	}
	types, err := api.chatDaemon.AgentTypeCatalog(ctx, opts)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, types)
}
