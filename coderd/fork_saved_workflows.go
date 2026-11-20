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
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpapi/httperror"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
)

const (
	maxForkSavedWorkflowNameBytes        = 128
	maxForkSavedWorkflowDescriptionBytes = 4096
	// Below the 16 KiB row limit, which counts the spaced jsonb text, so
	// one list_workflows result holds it.
	maxForkSavedWorkflowArgsSchemaBytes = 12 << 10
	maxForkSavedWorkflowPhases          = 32
	maxForkSavedWorkflowPhaseBytes      = 128
	// Each phase also takes two quotes and a comma.
	maxForkSavedWorkflowRequestBytes = (65536+maxForkSavedWorkflowDescriptionBytes+maxForkSavedWorkflowNameBytes)*skillJSONEscapeExpansion +
		maxForkSavedWorkflowPhases*(maxForkSavedWorkflowPhaseBytes*skillJSONEscapeExpansion+3) +
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
		CreatedBy:      httpmw.APIKey(r).UserID,
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
	name, ok := forkSavedWorkflowPathName(rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
		Name:           name,
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
// @Param expect_id query string false "Expected workflow ID, as a UUID" format(uuid)
// @Param request body codersdk.UpdateForkSavedWorkflowRequest true "Request body"
// @Failure 400 {object} codersdk.Response
// @Failure 409 {object} codersdk.Response
// @Success 200 {object} codersdk.ForkSavedWorkflow
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows/{workflow} [patch]
// @x-apidocgen {"skip": true}
func (api *API) patchForkUserSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	expectID, ok := readForkSavedWorkflowExpectID(rw, r)
	if !ok {
		return
	}
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
	update.ExpectID = expectID
	row, err := fdb.UpdateUserSavedWorkflowByName(ctx, update)
	probe := func(ctx context.Context) (uuid.UUID, error) {
		row, err := fdb.GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{
			OrganizationID: update.OrganizationID,
			UserID:         update.UserID,
			Name:           update.Name,
		})
		return row.ID, err
	}
	conflict := api.forkUserSavedWorkflowConflictProbe(r, update.OrganizationID, update.UserID, update.Name, policy.ActionUpdate)
	if !okAfterConditionalWrite(ctx, rw, err, expectID, false, probe, conflict) {
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
// @Param expect_id query string false "Expected workflow ID, as a UUID" format(uuid)
// @Failure 400 {object} codersdk.Response
// @Failure 409 {object} codersdk.Response
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows/{workflow} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkUserSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	member := httpmw.OrganizationMemberParam(r)
	name, ok := forkSavedWorkflowPathName(rw, r)
	if !ok {
		return
	}
	expectID, ok := readForkSavedWorkflowExpectID(rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	_, err := fdb.DeleteUserSavedWorkflowByName(ctx, forkdb.DeleteUserSavedWorkflowByNameParams{
		OrganizationID: member.OrganizationID,
		UserID:         member.UserID,
		Name:           name,
		ExpectID:       expectID,
	})
	probe := func(ctx context.Context) (uuid.UUID, error) {
		row, err := fdb.GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{
			OrganizationID: member.OrganizationID,
			UserID:         member.UserID,
			Name:           name,
		})
		return row.ID, err
	}
	conflict := api.forkUserSavedWorkflowConflictProbe(r, member.OrganizationID, member.UserID, name, policy.ActionDelete)
	if !okAfterConditionalWrite(ctx, rw, err, expectID, false, probe, conflict) {
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// forkUserSavedWorkflowConflictProbe reads the stored id of a personal
// workflow again for a conflict check. The plain probe keeps the caller's
// context, so a caller who cannot read the row keeps getting 404. The
// conflict probe must also answer a write-only API key scope, whose read
// would fail, so it fetches with a system-restricted context and authorizes
// the row it finds for the requested write action: a row the caller could
// not change answers 404, never a conflict that proves it exists.
func (api *API) forkUserSavedWorkflowConflictProbe(r *http.Request, organizationID, userID uuid.UUID, name string, action policy.Action) func(ctx context.Context) (uuid.UUID, error) {
	return func(ctx context.Context) (uuid.UUID, error) {
		fdb, ok := forkAgentDefinitionStore(nil, api.Database)
		if !ok {
			return uuid.UUID{}, xerrors.New("fork agent definition store unavailable")
		}
		//nolint:gocritic // The fetch only feeds the reauthorization below.
		row, err := fdb.GetUserSavedWorkflowByName(dbauthz.AsSystemRestricted(ctx), forkdb.GetUserSavedWorkflowByNameParams{
			OrganizationID: organizationID,
			UserID:         userID,
			Name:           name,
		})
		if err != nil {
			return uuid.UUID{}, err
		}
		if !api.Authorize(r, action, row) {
			return uuid.UUID{}, httperror.ErrResourceNotFound
		}
		return row.ID, nil
	}
}

// readForkSavedWorkflowExpectID reads the optional expect_id query
// parameter. Absent means no check. An explicit but invalid value, including
// an empty one, is a bad request, and so is a query string that does not
// parse, so a client can never silently drop its concurrency guard: Go's
// URL.Query drops a pair with a malformed escape such as %zz, and an
// unparsed query string cannot name one.
func readForkSavedWorkflowExpectID(rw http.ResponseWriter, r *http.Request) (uuid.NullUUID, bool) {
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		httpapi.Write(r.Context(), rw, http.StatusBadRequest, codersdk.Response{
			Message: "Invalid query string.",
			Detail:  "The query string does not parse, so expect_id may have been dropped.",
		})
		return uuid.NullUUID{}, false
	}
	raw, ok := r.URL.Query()["expect_id"]
	if !ok || len(raw) == 0 {
		return uuid.NullUUID{}, true
	}
	id, err := uuid.Parse(raw[0])
	if err != nil {
		httpapi.Write(r.Context(), rw, http.StatusBadRequest, codersdk.Response{
			Message: "Invalid expect_id.",
			Detail:  "expect_id must be a UUID.",
		})
		return uuid.NullUUID{}, false
	}
	return uuid.NullUUID{UUID: id, Valid: true}, true
}

// okAfterConditionalWrite turns the result of a conditional write into a
// response. Without an expect_id it reports the write error as usual. With
// one, sql.ErrNoRows can mean either a missing workflow (404) or a stored id
// that differs from the expected one (409), and currentID tells the two
// apart with the caller's authorization, so a caller who cannot read a
// hidden workflow gets 404 and never learns that it exists.
//
// The conflict check reads the row again with conflictID, which must
// reauthorize the row it finds for the requested write action: the write
// authorized the row as it was, and a replacement under the same name may
// hide from this caller, who then keeps getting 404 instead of a conflict
// that proves the replacement exists.
//
// A denial probes with currentID too, so a caller who cannot read a hidden
// workflow keeps getting 404, never a conflict or its stored id. Denials
// probe only when probeDenial is set or the write was conditional. The
// organization tier always probes, because its delete of a hidden workflow
// answered 404 before expect_id existed; the user tier keeps the plain
// write response unless the caller asked for the check.
//
//nolint:revive // probeDenial encodes a real tier difference, not a mode switch.
func okAfterConditionalWrite(ctx context.Context, rw http.ResponseWriter, err error, expectID uuid.NullUUID, probeDenial bool, currentID, conflictID func(ctx context.Context) (uuid.UUID, error)) bool {
	if err == nil {
		return true
	}
	if httpapi.IsUnauthorizedError(err) && (probeDenial || expectID.Valid) {
		_, readErr := currentID(ctx)
		if readErr == nil {
			// The workflow exists and the caller may read it, so its
			// denial was about the write: 403.
			writeForkSavedWorkflowWriteError(ctx, rw, err)
			return false
		}
		if httpapi.Is404Error(readErr) {
			// The caller cannot read the workflow either, so it learns
			// nothing beyond a missing resource: 404.
			httpapi.ResourceNotFound(rw)
			return false
		}
		// The probe itself failed, which says nothing about the workflow.
		httpapi.InternalServerError(rw, readErr)
		return false
	}
	if expectID.Valid && xerrors.Is(err, sql.ErrNoRows) {
		id, readErr := conflictID(ctx)
		switch {
		case readErr == nil && id != expectID.UUID:
			httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
				Message: "The stored workflow does not match the expected id.",
				Detail:  "Read the workflow again and retry with its current id.",
			})
			return false
		case httpapi.Is404Error(readErr):
			// The caller may not touch the row, whether it is gone or a
			// hidden replacement: the write error below answers 404.
		default:
			// The probe itself failed, which says nothing about the workflow.
			httpapi.InternalServerError(rw, readErr)
			return false
		}
	}
	writeForkSavedWorkflowWriteError(ctx, rw, err)
	return false
}

// forkSavedWorkflowPathName reads the workflow path parameter. A name that
// no saved workflow can have, such as one with a NUL byte, is not found and
// never reaches PostgreSQL.
func forkSavedWorkflowPathName(rw http.ResponseWriter, r *http.Request) (string, bool) {
	name := chi.URLParam(r, "workflow")
	if len(name) > maxForkSavedWorkflowNameBytes || !forkSavedWorkflowNameRegex.MatchString(name) {
		httpapi.ResourceNotFound(rw)
		return "", false
	}
	return name, true
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
	name, ok := forkSavedWorkflowPathName(rw, r)
	if !ok {
		return forkdb.UpdateUserSavedWorkflowByNameParams{}, false
	}
	update := forkdb.UpdateUserSavedWorkflowByNameParams{Name: name}
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
	// The description reaches the model through list_workflows, so it gets
	// the phase-name rule: no NUL, invalid UTF-8, control or format
	// characters, except line breaks and tabs, which a multi-line
	// description needs.
	if description != nil {
		if err := chatd.ValidateWorkflowDescription(*description); err != nil {
			return pqtype.NullRawMessage{}, xerrors.New("the description must not hold control, format or invalid characters")
		}
	}
	// PostgreSQL text and jsonb cannot hold a NUL character.
	if (description != nil && strings.ContainsRune(*description, 0)) ||
		(script != nil && strings.ContainsRune(*script, 0)) ||
		slices.ContainsFunc(phases, func(p string) bool { return strings.ContainsRune(p, 0) }) ||
		jsonHasNUL(argsSchema) {
		return pqtype.NullRawMessage{}, xerrors.New("the workflow must not contain NUL characters")
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
		if phase == "" || strings.TrimSpace(phase) != phase || len(phase) > maxForkSavedWorkflowPhaseBytes ||
			strings.ContainsFunc(phase, isForkSavedWorkflowPhaseHidden) {
			return pqtype.NullRawMessage{}, xerrors.Errorf("phase %q must be non-empty text without surrounding spaces, control or format characters, at most %d bytes", phase, maxForkSavedWorkflowPhaseBytes)
		}
		if seen[phase] {
			return pqtype.NullRawMessage{}, xerrors.Errorf("phase %q is declared twice", phase)
		}
		seen[phase] = true
	}
	if len(argsSchema) == 0 || bytes.Equal(bytes.TrimSpace(argsSchema), []byte("null")) {
		return pqtype.NullRawMessage{}, nil
	}
	// Bound the size before the schema check resolves the schema. Invalid
	// JSON fails to compact here and the schema check reports it.
	var compact bytes.Buffer
	if json.Compact(&compact, argsSchema) == nil && compact.Len() > maxForkSavedWorkflowArgsSchemaBytes {
		return pqtype.NullRawMessage{}, xerrors.Errorf("the args schema is larger than %d bytes", maxForkSavedWorkflowArgsSchemaBytes)
	}
	if err := chatd.ValidateWorkflowArgsSchema(argsSchema); err != nil {
		return pqtype.NullRawMessage{}, err
	}
	normalized, err := reencodeJSON(argsSchema)
	if err != nil {
		return pqtype.NullRawMessage{}, xerrors.Errorf("args schema: %w", err)
	}
	// The limit applies to the compact form. Whitespace does not count, and
	// invalid UTF-8 bytes come back as \ufffd escapes, so the size can grow.
	if len(normalized) > maxForkSavedWorkflowArgsSchemaBytes {
		return pqtype.NullRawMessage{}, xerrors.Errorf("the args schema is larger than %d bytes", maxForkSavedWorkflowArgsSchemaBytes)
	}
	return pqtype.NullRawMessage{RawMessage: normalized, Valid: true}, nil
}

// isForkSavedWorkflowPhaseHidden reports characters that a phase name must
// not hold: control characters, and format characters such as bidi
// overrides and zero-width spaces, which hide or reorder text.
func isForkSavedWorkflowPhaseHidden(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// reencodeJSON decodes raw and encodes it again. The decoder turns unpaired
// surrogate escapes and invalid UTF-8 into U+FFFD, so the result is text
// that jsonb accepts, while raw itself could fail the insert.
func reencodeJSON(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// jsonHasNUL reports whether a string or key of raw holds a NUL character.
// It stops at the first syntax error, which the schema check reports.
func jsonHasNUL(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if s, ok := tok.(string); ok && strings.ContainsRune(s, 0) {
			return true
		}
	}
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
	// The handlers check these limits first, but the database measures some
	// of them differently, for example args_schema as jsonb text.
	case database.IsCheckViolation(err,
		"saved_workflows_name_format", "saved_workflows_name_size",
		"saved_workflows_description_size", "saved_workflows_script_size",
		"saved_workflows_args_schema_is_object", "saved_workflows_args_schema_size",
		"saved_workflows_phases_size"):
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "The workflow breaks a storage constraint.",
			Detail:  err.Error(),
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
