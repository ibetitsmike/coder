package coderd

// This file is fork code: it serves the stored agent definitions of the
// fork-agent-definitions experiment. Writes are not audited, because auditing
// a fork type needs edits to upstream's audit packages.

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const maxForkAgentDefinitionRequestBytes = 65536*skillJSONEscapeExpansion + skillRequestEnvelopeBytes

// @Summary List user agent definitions
// @ID list-user-agent-definitions
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Success 200 {array} codersdk.ForkAgentDefinitionMetadata
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/agent-definitions [get]
func (api *API) forkUserAgentDefinitions(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	rows, err := fdb.ListUserAgentDefinitionMetadata(ctx, forkdb.ListUserAgentDefinitionMetadataParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
	})
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

// @Summary Create a user agent definition
// @ID create-a-user-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param request body codersdk.CreateForkAgentDefinitionRequest true "Request body"
// @Success 201 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/agent-definitions [post]
func (api *API) postForkUserAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	content, def, ok := readForkAgentDefinitionCreate(ctx, rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.InsertUserAgentDefinition(ctx, forkdb.InsertUserAgentDefinitionParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
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

// @Summary Get a user agent definition
// @ID get-a-user-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param agentDefinition path string true "Agent definition name"
// @Success 200 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/agent-definitions/{agentDefinition} [get]
func (api *API) forkUserAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.GetUserAgentDefinitionByName(ctx, forkdb.GetUserAgentDefinitionByNameParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
		Name:           chi.URLParam(r, "agentDefinition"),
	})
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkAgentDefinition(row))
}

// @Summary Update a user agent definition
// @ID update-a-user-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param agentDefinition path string true "Agent definition name"
// @Param request body codersdk.UpdateForkAgentDefinitionRequest true "Request body"
// @Success 200 {object} codersdk.ForkAgentDefinition
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/agent-definitions/{agentDefinition} [patch]
func (api *API) patchForkUserAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	update, ok := readForkAgentDefinitionUpdate(ctx, rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	update.OrganizationID = member.OrganizationID
	update.UserID = member.UserID
	row, err := fdb.UpdateUserAgentDefinitionByName(ctx, update)
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkAgentDefinition(row))
}

// @Summary Delete a user agent definition
// @ID delete-a-user-agent-definition
// @Security CoderSessionToken
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param agentDefinition path string true "Agent definition name"
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/agent-definitions/{agentDefinition} [delete]
func (api *API) deleteForkUserAgentDefinition(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	_, err := fdb.DeleteUserAgentDefinitionByName(ctx, forkdb.DeleteUserAgentDefinitionByNameParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
		Name:           chi.URLParam(r, "agentDefinition"),
	})
	if err != nil {
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func forkAgentDefinitionStore(rw http.ResponseWriter, db database.Store) (forkdb.Store, bool) {
	fdb, err := forkdb.From(db)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return nil, false
	}
	return fdb, true
}

// readForkAgentDefinitionCreate reads a create request and parses its
// content. A stored definition has no file name, so its front-matter must
// name it.
func readForkAgentDefinitionCreate(ctx context.Context, rw http.ResponseWriter, r *http.Request) (string, workspacesdk.AgentDefinition, bool) {
	var req codersdk.CreateForkAgentDefinitionRequest
	if !httpapi.ReadLimit(ctx, rw, r, maxForkAgentDefinitionRequestBytes, &req) {
		return "", workspacesdk.AgentDefinition{}, false
	}
	def, ok := parseForkAgentDefinition(ctx, rw, req.Content)
	return req.Content, def, ok
}

// readForkAgentDefinitionUpdate reads a PATCH request for the definition
// named in the path. New content must keep that name.
func readForkAgentDefinitionUpdate(ctx context.Context, rw http.ResponseWriter, r *http.Request) (forkdb.UpdateUserAgentDefinitionByNameParams, bool) {
	update := forkdb.UpdateUserAgentDefinitionByNameParams{Name: chi.URLParam(r, "agentDefinition")}
	var req codersdk.UpdateForkAgentDefinitionRequest
	if !httpapi.ReadLimit(ctx, rw, r, maxForkAgentDefinitionRequestBytes, &req) {
		return update, false
	}
	if req.Content == nil && req.Enabled == nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "No agent definition fields to update.",
			Detail:  "Set content, enabled, or both.",
		})
		return update, false
	}
	update.Enabled = nullBool(req.Enabled)
	if req.Content == nil {
		return update, true
	}
	def, ok := parseForkAgentDefinition(ctx, rw, *req.Content)
	if !ok {
		return update, false
	}
	if def.Name != update.Name {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "The agent definition name in the path does not match the front-matter name.",
		})
		return update, false
	}
	update.Description = sql.NullString{String: def.Description, Valid: true}
	update.Content = sql.NullString{String: *req.Content, Valid: true}
	return update, true
}

func parseForkAgentDefinition(ctx context.Context, rw http.ResponseWriter, content string) (workspacesdk.AgentDefinition, bool) {
	def, err := workspacesdk.ParseAgentDefinition(content, "")
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Invalid agent definition content.",
			Detail:  err.Error(),
		})
		return workspacesdk.AgentDefinition{}, false
	}
	return def, true
}

func writeForkAgentDefinitionReadError(rw http.ResponseWriter, err error) {
	if httpapi.Is404Error(err) {
		httpapi.ResourceNotFound(rw)
		return
	}
	httpapi.InternalServerError(rw, err)
}

func writeForkAgentDefinitionWriteError(ctx context.Context, rw http.ResponseWriter, err error) {
	switch {
	case httpapi.IsUnauthorizedError(err):
		httpapi.Write(ctx, rw, http.StatusForbidden, codersdk.Response{
			Message: "You don't have permission to modify this agent definition.",
		})
	case httpapi.Is404Error(err):
		httpapi.ResourceNotFound(rw)
	case database.IsUniqueViolation(err, "agent_definitions_user_name_idx", "agent_definitions_organization_name_idx"):
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "An agent definition with this name already exists.",
		})
	// The limit trigger raises these names with USING CONSTRAINT.
	case database.IsCheckViolation(err, "agent_definitions_per_user_limit", "agent_definitions_per_organization_limit"):
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "Agent definition limit reached.",
			Detail:  "A user or an organization can have at most 100 agent definitions.",
		})
	case database.IsCheckViolation(err, "agent_definitions_name_size", "agent_definitions_description_size", "agent_definitions_content_size"):
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Agent definition is too large.",
			Detail:  "The name can have at most 256 bytes, the description 4096 and the content 65536.",
		})
	default:
		httpapi.InternalServerError(rw, err)
	}
}

func convertForkAgentDefinition(row forkdb.AgentDefinition) codersdk.ForkAgentDefinition {
	return codersdk.ForkAgentDefinition{
		ForkAgentDefinitionMetadata: codersdk.ForkAgentDefinitionMetadata{
			ID: row.ID, OrganizationID: row.OrganizationID, Name: row.Name, Description: row.Description,
			Enabled: row.Enabled, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		},
		Content: row.Content,
	}
}
