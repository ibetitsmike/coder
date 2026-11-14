package coderd

// This file is fork code: it serves the saved workflows of the
// fork-workflows experiment. Users save workflows here; agents only run them
// by name. Writes are not audited, because auditing a fork type needs edits
// to upstream's audit packages.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
)

const (
	maxForkSavedWorkflowNameBytes        = 128
	maxForkSavedWorkflowDescriptionBytes = 4096
	maxForkSavedWorkflowArgsSchemaBytes  = 16384
	maxForkSavedWorkflowPhases           = 32
	maxForkSavedWorkflowPhaseBytes       = 128
	maxForkSavedWorkflowRequestBytes     = (65536+maxForkSavedWorkflowDescriptionBytes)*skillJSONEscapeExpansion +
		maxForkSavedWorkflowArgsSchemaBytes + skillRequestEnvelopeBytes
)

var forkSavedWorkflowNameRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// @Summary List user saved workflows
// @ID list-user-saved-workflows
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Success 200 {array} codersdk.ForkSavedWorkflowMetadata
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows [get]
// @x-apidocgen {"skip": true}
func (api *API) forkUserSavedWorkflows(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	rows, err := fdb.ListUserSavedWorkflowMetadata(ctx, forkdb.ListUserSavedWorkflowMetadataParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
	})
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return
	}
	metadata := make([]codersdk.ForkSavedWorkflowMetadata, 0, len(rows))
	for _, row := range rows {
		metadata = append(metadata, convertForkSavedWorkflowMetadata(forkdb.SavedWorkflow{
			ID: row.ID, OrganizationID: row.OrganizationID, UserID: row.UserID, CreatedBy: row.CreatedBy,
			Name: row.Name, Description: row.Description, ArgsSchema: row.ArgsSchema, Phases: row.Phases,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}))
	}
	httpapi.Write(ctx, rw, http.StatusOK, metadata)
}

// @Summary Create a user saved workflow
// @ID create-a-user-saved-workflow
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param request body codersdk.CreateForkSavedWorkflowRequest true "Request body"
// @Success 201 {object} codersdk.ForkSavedWorkflow
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows [post]
// @x-apidocgen {"skip": true}
func (api *API) postForkUserSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	req, argsSchema, ok := readForkSavedWorkflowCreate(ctx, rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
		Name:           req.Name,
		Description:    req.Description,
		Script:         req.Script,
		ArgsSchema:     argsSchema,
		Phases:         req.Phases,
	})
	if err != nil {
		writeForkSavedWorkflowWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusCreated, convertForkSavedWorkflow(row))
}

// @Summary Get a user saved workflow
// @ID get-a-user-saved-workflow
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param workflow path string true "Workflow name"
// @Success 200 {object} codersdk.ForkSavedWorkflow
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows/{workflow} [get]
// @x-apidocgen {"skip": true}
func (api *API) forkUserSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
		Name:           chi.URLParam(r, "workflow"),
	})
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkSavedWorkflow(row))
}

// @Summary Update a user saved workflow
// @ID update-a-user-saved-workflow
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param workflow path string true "Workflow name"
// @Param request body codersdk.UpdateForkSavedWorkflowRequest true "Request body"
// @Success 200 {object} codersdk.ForkSavedWorkflow
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows/{workflow} [patch]
// @x-apidocgen {"skip": true}
func (api *API) patchForkUserSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	update, ok := readForkSavedWorkflowUpdate(ctx, rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	update.OrganizationID = member.OrganizationID
	update.UserID = member.UserID
	row, err := fdb.UpdateUserSavedWorkflowByName(ctx, update)
	if err != nil {
		writeForkSavedWorkflowWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkSavedWorkflow(row))
}

// @Summary Delete a user saved workflow
// @ID delete-a-user-saved-workflow
// @Security CoderSessionToken
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param workflow path string true "Workflow name"
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows/{workflow} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkUserSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	_, err := fdb.DeleteUserSavedWorkflowByName(ctx, forkdb.DeleteUserSavedWorkflowByNameParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
		Name:           chi.URLParam(r, "workflow"),
	})
	if err != nil {
		writeForkSavedWorkflowWriteError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// readForkSavedWorkflowCreate reads and checks a create request.
func readForkSavedWorkflowCreate(ctx context.Context, rw http.ResponseWriter, r *http.Request) (codersdk.CreateForkSavedWorkflowRequest, pqtype.NullRawMessage, bool) {
	var req codersdk.CreateForkSavedWorkflowRequest
	if !httpapi.ReadLimit(ctx, rw, r, maxForkSavedWorkflowRequestBytes, &req) {
		return req, pqtype.NullRawMessage{}, false
	}
	argsSchema, err := checkForkSavedWorkflow(req.Name, &req.Description, &req.Script, req.ArgsSchema, req.Phases)
	if err != nil {
		writeForkSavedWorkflowInvalid(ctx, rw, err)
		return req, pqtype.NullRawMessage{}, false
	}
	return req, argsSchema, true
}

// readForkSavedWorkflowUpdate reads and checks a PATCH request for the
// workflow named in the path.
func readForkSavedWorkflowUpdate(ctx context.Context, rw http.ResponseWriter, r *http.Request) (forkdb.UpdateUserSavedWorkflowByNameParams, bool) {
	update := forkdb.UpdateUserSavedWorkflowByNameParams{Name: chi.URLParam(r, "workflow")}
	var req codersdk.UpdateForkSavedWorkflowRequest
	if !httpapi.ReadLimit(ctx, rw, r, maxForkSavedWorkflowRequestBytes, &req) {
		return update, false
	}
	if req.Description == nil && req.Script == nil && len(req.ArgsSchema) == 0 && req.Phases == nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "No workflow fields to update.",
			Detail:  "Set description, script, args_schema or phases.",
		})
		return update, false
	}
	var phases []string
	if req.Phases != nil {
		phases = *req.Phases
	}
	argsSchema, err := checkForkSavedWorkflow(update.Name, req.Description, req.Script, req.ArgsSchema, phases)
	if err != nil {
		writeForkSavedWorkflowInvalid(ctx, rw, err)
		return update, false
	}
	if req.Description != nil {
		update.Description = sql.NullString{String: *req.Description, Valid: true}
	}
	if req.Script != nil {
		update.Script = sql.NullString{String: *req.Script, Valid: true}
	}
	update.SetArgsSchema = len(req.ArgsSchema) > 0
	update.ArgsSchema = argsSchema
	update.Phases = phases
	return update, true
}

// checkForkSavedWorkflow checks the fields of a create or update. A nil
// pointer or slice is a field the request leaves alone. It returns the args
// schema to store, which is NULL for an absent or null schema.
func checkForkSavedWorkflow(name string, description, script *string, argsSchema json.RawMessage, phases []string) (pqtype.NullRawMessage, error) {
	if len(name) > maxForkSavedWorkflowNameBytes || !forkSavedWorkflowNameRegex.MatchString(name) {
		return pqtype.NullRawMessage{}, xerrors.Errorf("the name must be lowercase letters and digits in dash-separated words, at most %d bytes", maxForkSavedWorkflowNameBytes)
	}
	if description != nil && len(*description) > maxForkSavedWorkflowDescriptionBytes {
		return pqtype.NullRawMessage{}, xerrors.Errorf("the description is longer than %d bytes", maxForkSavedWorkflowDescriptionBytes)
	}
	if script != nil {
		if err := chatd.ValidateWorkflowScript(*script); err != nil {
			return pqtype.NullRawMessage{}, err
		}
	}
	if len(phases) > maxForkSavedWorkflowPhases {
		return pqtype.NullRawMessage{}, xerrors.Errorf("a workflow can declare at most %d phases", maxForkSavedWorkflowPhases)
	}
	seen := make(map[string]bool, len(phases))
	for _, phase := range phases {
		if phase == "" || strings.TrimSpace(phase) != phase || len(phase) > maxForkSavedWorkflowPhaseBytes {
			return pqtype.NullRawMessage{}, xerrors.Errorf("phase %q must be non-empty text without surrounding spaces, at most %d bytes", phase, maxForkSavedWorkflowPhaseBytes)
		}
		if seen[phase] {
			return pqtype.NullRawMessage{}, xerrors.Errorf("phase %q is declared twice", phase)
		}
		seen[phase] = true
	}
	if len(argsSchema) == 0 || bytes.Equal(bytes.TrimSpace(argsSchema), []byte("null")) {
		return pqtype.NullRawMessage{}, nil
	}
	if len(argsSchema) > maxForkSavedWorkflowArgsSchemaBytes {
		return pqtype.NullRawMessage{}, xerrors.Errorf("the args schema is larger than %d bytes", maxForkSavedWorkflowArgsSchemaBytes)
	}
	if err := chatd.ValidateWorkflowArgsSchema(argsSchema); err != nil {
		return pqtype.NullRawMessage{}, err
	}
	return pqtype.NullRawMessage{RawMessage: argsSchema, Valid: true}, nil
}

func writeForkSavedWorkflowInvalid(ctx context.Context, rw http.ResponseWriter, err error) {
	httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
		Message: "Invalid saved workflow.",
		Detail:  err.Error(),
	})
}

func writeForkSavedWorkflowWriteError(ctx context.Context, rw http.ResponseWriter, err error) {
	switch {
	case httpapi.IsUnauthorizedError(err):
		httpapi.Write(ctx, rw, http.StatusForbidden, codersdk.Response{
			Message: "You don't have permission to modify this workflow.",
		})
	case httpapi.Is404Error(err):
		httpapi.ResourceNotFound(rw)
	case database.IsUniqueViolation(err, "saved_workflows_user_name_idx", "saved_workflows_organization_name_idx"):
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "A workflow with this name already exists.",
		})
	// The limit trigger raises these names with USING CONSTRAINT.
	case database.IsCheckViolation(err, "saved_workflows_per_user_limit", "saved_workflows_per_organization_limit"):
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "Saved workflow limit reached.",
			Detail:  "A user or an organization can have at most 100 saved workflows.",
		})
	default:
		httpapi.InternalServerError(rw, err)
	}
}

func convertForkSavedWorkflowMetadata(row forkdb.SavedWorkflow) codersdk.ForkSavedWorkflowMetadata {
	phases := row.Phases
	if phases == nil {
		phases = []string{}
	}
	metadata := codersdk.ForkSavedWorkflowMetadata{
		ID: row.ID, OrganizationID: row.OrganizationID, Name: row.Name, Description: row.Description,
		Phases: phases, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		UserID: nullUUIDPtr(row.UserID), CreatedBy: nullUUIDPtr(row.CreatedBy),
	}
	if row.ArgsSchema.Valid {
		metadata.ArgsSchema = row.ArgsSchema.RawMessage
	}
	return metadata
}

func convertForkSavedWorkflow(row forkdb.SavedWorkflow) codersdk.ForkSavedWorkflow {
	return codersdk.ForkSavedWorkflow{ForkSavedWorkflowMetadata: convertForkSavedWorkflowMetadata(row), Script: row.Script}
}

func nullUUIDPtr(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	return &id.UUID
}
