package coderd

// This file is fork code: it serves the organization-tier agent definitions
// of the fork-agent-definitions experiment.

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

// @Summary List organization agent definitions
// @ID list-organization-agent-definitions
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Success 200 {array} codersdk.ForkAgentDefinitionMetadata
// @Router /api/v2/chats/fork/organizations/{organization}/agent-definitions [get]
func (api *API) forkOrganizationAgentDefinitions(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	rows, err := fdb.ListOrganizationAgentDefinitionMetadata(ctx, httpmw.OrganizationParam(r).ID)
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

// @Summary Create an organization agent definition
// @ID create-an-organization-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param request body codersdk.CreateForkAgentDefinitionRequest true "Request body"
// @Success 201 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/agent-definitions [post]
func (api *API) postForkOrganizationAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	content, def, ok := readForkAgentDefinitionCreate(ctx, rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.InsertOrganizationAgentDefinition(ctx, forkdb.InsertOrganizationAgentDefinitionParams{
		OrganizationID: organization.ID,
		Name:           def.Name,
		Description:    def.Description,
		Content:        content,
		// The Everyone group shares the organization's ID, so a new
		// definition reaches every member, like a new organization skill.
		GroupAcl: database.ChatACL{
			organization.ID.String(): {Permissions: []policy.Action{policy.ActionRead}},
		},
		UserAcl: database.ChatACL{},
	})
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusCreated, convertForkAgentDefinition(row))
}

// @Summary Get an organization agent definition
// @ID get-an-organization-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param agentDefinition path string true "Agent definition name"
// @Success 200 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition} [get]
func (api *API) forkOrganizationAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, ok := api.readForkOrganizationAgentDefinition(rw, r)
	if !ok {
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkAgentDefinition(row))
}

// @Summary Update an organization agent definition
// @ID update-an-organization-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param agentDefinition path string true "Agent definition name"
// @Param request body codersdk.UpdateForkAgentDefinitionRequest true "Request body"
// @Success 200 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition} [patch]
func (api *API) patchForkOrganizationAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	current, ok := api.readForkOrganizationAgentDefinition(rw, r)
	if !ok {
		return
	}
	update, ok := readForkAgentDefinitionUpdate(ctx, rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.UpdateOrganizationAgentDefinitionByName(ctx, forkdb.UpdateOrganizationAgentDefinitionByNameParams{
		Description:    update.Description,
		Content:        update.Content,
		Enabled:        update.Enabled,
		OrganizationID: current.OrganizationID,
		Name:           current.Name,
	})
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkAgentDefinition(row))
}

// @Summary Delete an organization agent definition
// @ID delete-an-organization-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param agentDefinition path string true "Agent definition name"
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition} [delete]
func (api *API) deleteForkOrganizationAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	current, ok := api.readForkOrganizationAgentDefinition(rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	_, err := fdb.DeleteOrganizationAgentDefinitionByName(ctx, forkdb.DeleteOrganizationAgentDefinitionByNameParams{
		OrganizationID: current.OrganizationID,
		Name:           current.Name,
	})
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// readForkOrganizationAgentDefinition reads the organization definition
// named in the path. Updates and deletes read it first so that a caller who
// cannot read the definition gets 404, and only readers get 403.
func (api *API) readForkOrganizationAgentDefinition(rw http.ResponseWriter, r *http.Request) (forkdb.AgentDefinition, bool) {
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return forkdb.AgentDefinition{}, false
	}
	row, err := fdb.GetOrganizationAgentDefinitionByName(r.Context(), forkdb.GetOrganizationAgentDefinitionByNameParams{
		OrganizationID: httpmw.OrganizationParam(r).ID,
		Name:           chi.URLParam(r, "agentDefinition"),
	})
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return forkdb.AgentDefinition{}, false
	}
	return row, true
}
