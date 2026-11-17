package coderd

// This file is fork code: it serves the organization-tier agent definitions
// of the fork-agent-definitions experiment.

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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
// @x-apidocgen {"skip": true}
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
// @x-apidocgen {"skip": true}
func (api *API) postForkOrganizationAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	fa, commitAudit := api.initForkAudit(rw, r, "organization_agent_definition", forkAuditCreate)
	defer commitAudit()
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
	fa.target(organization, row.ID, row.Name)
	fa.diff(nil, forkAuditAgentDefinition(row))
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
// @x-apidocgen {"skip": true}
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
// @x-apidocgen {"skip": true}
func (api *API) patchForkOrganizationAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fa, commitAudit := api.initForkAudit(rw, r, "organization_agent_definition", forkAuditUpdate)
	defer commitAudit()
	current, ok := api.readForkOrganizationAgentDefinition(rw, r)
	if !ok {
		return
	}
	fa.target(httpmw.OrganizationParam(r), current.ID, current.Name)
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
	fa.diff(forkAuditAgentDefinition(current), forkAuditAgentDefinition(row))
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
// @x-apidocgen {"skip": true}
func (api *API) deleteForkOrganizationAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fa, commitAudit := api.initForkAudit(rw, r, "organization_agent_definition", forkAuditDelete)
	defer commitAudit()
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	fa.target(httpmw.OrganizationParam(r), uuid.Nil, chi.URLParam(r, "agentDefinition"))
	// Delete authorizes before reading so that delete-only API key scopes
	// work. A caller who can neither delete nor read gets 404.
	deleted, err := fdb.DeleteOrganizationAgentDefinitionByName(ctx, forkdb.DeleteOrganizationAgentDefinitionByNameParams{
		OrganizationID: httpmw.OrganizationParam(r).ID,
		Name:           chi.URLParam(r, "agentDefinition"),
	})
	if httpapi.IsUnauthorizedError(err) {
		if _, ok := api.readForkOrganizationAgentDefinition(rw, r); !ok {
			return
		}
	}
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	fa.target(httpmw.OrganizationParam(r), deleted.ID, deleted.Name)
	fa.diff(forkAuditAgentDefinition(deleted), nil)
	rw.WriteHeader(http.StatusNoContent)
}

// forkAuditAgentDefinition is the audit view of an organization agent
// definition.
func forkAuditAgentDefinition(row forkdb.AgentDefinition) any {
	return struct {
		Name        string           `json:"name"`
		Description string           `json:"description"`
		Content     string           `json:"content"`
		Enabled     bool             `json:"enabled"`
		GroupACL    database.ChatACL `json:"group_acl"`
		UserACL     database.ChatACL `json:"user_acl"`
	}{row.Name, row.Description, row.Content, row.Enabled, row.GroupAcl, row.UserAcl}
}

// forkAuditProjectAgentDefinition is the audit view of a project agent
// definition. It omits the content, because auditors can read the audit log
// without being able to read a chat project.
func forkAuditProjectAgentDefinition(row forkdb.AgentDefinition) any {
	return struct {
		Name        string           `json:"name"`
		Description string           `json:"description"`
		Enabled     bool             `json:"enabled"`
		GroupACL    database.ChatACL `json:"group_acl"`
		UserACL     database.ChatACL `json:"user_acl"`
	}{row.Name, row.Description, row.Enabled, row.GroupAcl, row.UserAcl}
}

// forkAuditUserAgentDefinition is the audit view of a personal agent
// definition. It omits the content, because auditors can read the audit log
// without being able to read another user's personal skill, and names the
// owner, because a deleted personal definition is otherwise not traceable.
func forkAuditUserAgentDefinition(row forkdb.AgentDefinition) any {
	return struct {
		UserID      uuid.NullUUID `json:"user_id"`
		Name        string        `json:"name"`
		Description string        `json:"description"`
		Enabled     bool          `json:"enabled"`
	}{row.UserID, row.Name, row.Description, row.Enabled}
}

// readForkOrganizationAgentDefinition reads the organization definition
// named in the path. Updates read it first so that a caller who
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
