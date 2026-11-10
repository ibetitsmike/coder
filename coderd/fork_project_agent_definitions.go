package coderd

// This file is fork code: it serves the project-tier agent definitions of the
// fork-agent-definitions experiment. Like the project skill routes, readers of
// the project can read its definitions, and only callers who can update the
// project can change them. Everyone else gets 404.

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary List project agent definitions
// @ID list-project-agent-definitions
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param project path string true "Chat project ID" format(uuid)
// @Success 200 {array} codersdk.ForkAgentDefinitionMetadata
// @Router /api/v2/chats/fork/organizations/{organization}/projects/{project}/agent-definitions [get]
// @x-apidocgen {"skip": true}
func (api *API) forkProjectAgentDefinitions(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, fdb, ok := api.forkProjectAgentDefinitionAccess(rw, r, policy.ActionRead)
	if !ok {
		return
	}
	rows, err := fdb.ListProjectAgentDefinitionMetadata(ctx, project.ID)
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return
	}
	metadata := make([]codersdk.ForkAgentDefinitionMetadata, 0, len(rows))
	for _, row := range rows {
		metadata = append(metadata, codersdk.ForkAgentDefinitionMetadata{
			ID: row.ID, OrganizationID: row.OrganizationID, Name: row.Name, Description: row.Description,
			Enabled: row.Enabled, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	httpapi.Write(ctx, rw, http.StatusOK, metadata)
}

// @Summary Create a project agent definition
// @ID create-a-project-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param project path string true "Chat project ID" format(uuid)
// @Param request body codersdk.CreateForkAgentDefinitionRequest true "Request body"
// @Success 201 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/projects/{project}/agent-definitions [post]
// @x-apidocgen {"skip": true}
func (api *API) postForkProjectAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, fdb, ok := api.forkProjectAgentDefinitionAccess(rw, r, policy.ActionUpdate)
	if !ok {
		return
	}
	content, def, ok := readForkAgentDefinitionCreate(ctx, rw, r)
	if !ok {
		return
	}
	row, err := fdb.InsertProjectAgentDefinition(ctx, forkdb.InsertProjectAgentDefinitionParams{
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		Name:           def.Name,
		Description:    def.Description,
		Content:        content,
	})
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusCreated, convertForkAgentDefinition(row))
}

// @Summary Get a project agent definition
// @ID get-a-project-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param project path string true "Chat project ID" format(uuid)
// @Param agentDefinition path string true "Agent definition name"
// @Success 200 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/projects/{project}/agent-definitions/{agentDefinition} [get]
// @x-apidocgen {"skip": true}
func (api *API) forkProjectAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, fdb, ok := api.forkProjectAgentDefinitionAccess(rw, r, policy.ActionRead)
	if !ok {
		return
	}
	row, err := fdb.GetProjectAgentDefinitionByName(ctx, forkdb.GetProjectAgentDefinitionByNameParams{
		ProjectID: project.ID,
		Name:      chi.URLParam(r, "agentDefinition"),
	})
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkAgentDefinition(row))
}

// @Summary Update a project agent definition
// @ID update-a-project-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param project path string true "Chat project ID" format(uuid)
// @Param agentDefinition path string true "Agent definition name"
// @Param request body codersdk.UpdateForkAgentDefinitionRequest true "Request body"
// @Success 200 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/projects/{project}/agent-definitions/{agentDefinition} [patch]
// @x-apidocgen {"skip": true}
func (api *API) patchForkProjectAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, fdb, ok := api.forkProjectAgentDefinitionAccess(rw, r, policy.ActionUpdate)
	if !ok {
		return
	}
	update, ok := readForkAgentDefinitionUpdate(ctx, rw, r)
	if !ok {
		return
	}
	row, err := fdb.UpdateProjectAgentDefinitionByName(ctx, forkdb.UpdateProjectAgentDefinitionByNameParams{
		Description: update.Description,
		Content:     update.Content,
		Enabled:     update.Enabled,
		ProjectID:   project.ID,
		Name:        update.Name,
	})
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkAgentDefinition(row))
}

// @Summary Delete a project agent definition
// @ID delete-a-project-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param project path string true "Chat project ID" format(uuid)
// @Param agentDefinition path string true "Agent definition name"
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/projects/{project}/agent-definitions/{agentDefinition} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkProjectAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, fdb, ok := api.forkProjectAgentDefinitionAccess(rw, r, policy.ActionUpdate)
	if !ok {
		return
	}
	_, err := fdb.DeleteProjectAgentDefinitionByName(ctx, forkdb.DeleteProjectAgentDefinitionByNameParams{
		ProjectID: project.ID,
		Name:      chi.URLParam(r, "agentDefinition"),
	})
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// forkProjectAgentDefinitionAccess checks the action on the project in the
// path before the request body is read, like startChatProjectSkillWrite.
func (api *API) forkProjectAgentDefinitionAccess(rw http.ResponseWriter, r *http.Request, action policy.Action) (database.ChatProject, forkdb.Store, bool) {
	project := httpmw.ChatProjectParam(r)
	if !api.Authorize(r, action, project.RBACObject()) {
		httpapi.ResourceNotFound(rw)
		return database.ChatProject{}, nil, false
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	return project, fdb, ok
}
