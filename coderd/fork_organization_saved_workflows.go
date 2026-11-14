package coderd

// This file is fork code: it serves the organization-tier saved workflows of
// the fork-workflows experiment and their ACLs. The ACL routes reuse the
// request and response types of the organization skill ACL routes.

import (
	"errors"
	"maps"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary List organization saved workflows
// @ID list-organization-saved-workflows
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Success 200 {array} codersdk.ForkSavedWorkflowMetadata
// @Router /api/v2/chats/fork/organizations/{organization}/workflows [get]
// @x-apidocgen {"skip": true}
func (api *API) forkOrganizationSavedWorkflows(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	rows, err := fdb.ListOrganizationSavedWorkflowMetadata(ctx, httpmw.OrganizationParam(r).ID)
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return
	}
	metadata := make([]codersdk.ForkSavedWorkflowMetadata, 0, len(rows))
	for _, row := range rows {
		metadata = append(metadata, convertForkSavedWorkflowMetadata(forkdb.SavedWorkflow{
			ID: row.ID, OrganizationID: row.OrganizationID, CreatedBy: row.CreatedBy,
			Name: row.Name, Description: row.Description, ArgsSchema: row.ArgsSchema, Phases: row.Phases,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}))
	}
	httpapi.Write(ctx, rw, http.StatusOK, metadata)
}

// @Summary Create an organization saved workflow
// @ID create-an-organization-saved-workflow
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param request body codersdk.CreateForkSavedWorkflowRequest true "Request body"
// @Success 201 {object} codersdk.ForkSavedWorkflow
// @Router /api/v2/chats/fork/organizations/{organization}/workflows [post]
// @x-apidocgen {"skip": true}
func (api *API) postForkOrganizationSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	req, argsSchema, ok := readForkSavedWorkflowCreate(ctx, rw, r)
	if !ok {
		return
	}
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.InsertOrganizationSavedWorkflow(ctx, forkdb.InsertOrganizationSavedWorkflowParams{
		OrganizationID: organization.ID,
		CreatedBy:      httpmw.APIKey(r).UserID,
		Name:           req.Name,
		Description:    req.Description,
		Script:         req.Script,
		ArgsSchema:     argsSchema,
		Phases:         req.Phases,
		// The Everyone group shares the organization's ID, so a new
		// workflow reaches every member, like a new organization skill.
		GroupAcl: database.ChatACL{
			organization.ID.String(): {Permissions: []policy.Action{policy.ActionRead}},
		},
		UserAcl: database.ChatACL{},
	})
	if err != nil {
		writeForkSavedWorkflowWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusCreated, convertForkSavedWorkflow(row))
}

// @Summary Get an organization saved workflow
// @ID get-an-organization-saved-workflow
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param workflow path string true "Workflow name"
// @Success 200 {object} codersdk.ForkSavedWorkflow
// @Router /api/v2/chats/fork/organizations/{organization}/workflows/{workflow} [get]
// @x-apidocgen {"skip": true}
func (api *API) forkOrganizationSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	row, ok := api.readForkOrganizationSavedWorkflow(rw, r)
	if !ok {
		return
	}
	httpapi.Write(r.Context(), rw, http.StatusOK, convertForkSavedWorkflow(row))
}

// @Summary Update an organization saved workflow
// @ID update-an-organization-saved-workflow
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param workflow path string true "Workflow name"
// @Param request body codersdk.UpdateForkSavedWorkflowRequest true "Request body"
// @Success 200 {object} codersdk.ForkSavedWorkflow
// @Router /api/v2/chats/fork/organizations/{organization}/workflows/{workflow} [patch]
// @x-apidocgen {"skip": true}
func (api *API) patchForkOrganizationSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	current, ok := api.readForkOrganizationSavedWorkflow(rw, r)
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
	row, err := fdb.UpdateOrganizationSavedWorkflowByName(ctx, forkdb.UpdateOrganizationSavedWorkflowByNameParams{
		Description:    update.Description,
		Script:         update.Script,
		SetArgsSchema:  update.SetArgsSchema,
		ArgsSchema:     update.ArgsSchema,
		Phases:         update.Phases,
		OrganizationID: current.OrganizationID,
		Name:           current.Name,
	})
	if err != nil {
		writeForkSavedWorkflowWriteError(ctx, rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkSavedWorkflow(row))
}

// @Summary Delete an organization saved workflow
// @ID delete-an-organization-saved-workflow
// @Security CoderSessionToken
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param workflow path string true "Workflow name"
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/workflows/{workflow} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkOrganizationSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return
	}
	// Delete authorizes before reading so that delete-only API key scopes
	// work. A caller who can neither delete nor read gets 404.
	_, err := fdb.DeleteOrganizationSavedWorkflowByName(ctx, forkdb.DeleteOrganizationSavedWorkflowByNameParams{
		OrganizationID: httpmw.OrganizationParam(r).ID,
		Name:           chi.URLParam(r, "workflow"),
	})
	if httpapi.IsUnauthorizedError(err) {
		if _, ok := api.readForkOrganizationSavedWorkflow(rw, r); !ok {
			return
		}
	}
	if err != nil {
		writeForkSavedWorkflowWriteError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// @Summary Get an organization saved workflow ACL
// @ID get-an-organization-saved-workflow-acl
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param workflow path string true "Workflow name"
// @Success 200 {object} codersdk.OrganizationSkillACL
// @Router /api/v2/chats/fork/organizations/{organization}/workflows/{workflow}/acl [get]
// @x-apidocgen {"skip": true}
func (api *API) forkOrganizationSavedWorkflowACL(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, ok := api.readForkOrganizationSavedWorkflowForShare(rw, r)
	if !ok {
		return
	}
	users, err := resolveACLUsers(ctx, api.Database, row.UserAcl, func(user codersdk.MinimalUser, _ database.ChatACLEntry) codersdk.OrganizationSkillUser {
		return codersdk.OrganizationSkillUser{MinimalUser: user, Role: codersdk.OrganizationSkillRoleRead}
	})
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	groups, err := resolveACLGroups(ctx, api.Database, uuid.Nil, row.GroupAcl, func(group codersdk.Group, _ database.ChatACLEntry) codersdk.OrganizationSkillGroup {
		return codersdk.OrganizationSkillGroup{Group: group, Role: codersdk.OrganizationSkillRoleRead}
	})
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.OrganizationSkillACL{Users: users, Groups: groups})
}

// @Summary Get available organization saved workflow ACL users and groups
// @ID get-available-organization-saved-workflow-acl-users-and-groups
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param workflow path string true "Workflow name"
// @Param q query string false "User search query; free-text search also applies to groups"
// @Param after_id query string false "User after ID" format(uuid)
// @Param limit query int false "Page limit for users and groups, if 0 returns all candidates"
// @Param offset query int false "User page offset"
// @Success 200 {object} codersdk.ACLAvailable
// @Router /api/v2/chats/fork/organizations/{organization}/workflows/{workflow}/acl/available [get]
// @x-apidocgen {"skip": true}
func (api *API) forkOrganizationSavedWorkflowACLAvailable(rw http.ResponseWriter, r *http.Request) {
	row, ok := api.readForkOrganizationSavedWorkflowForShare(rw, r)
	if !ok {
		return
	}
	api.writeOrganizationACLAvailable(rw, r, row.OrganizationID)
}

// @Summary Update an organization saved workflow ACL
// @ID update-an-organization-saved-workflow-acl
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Param organization path string true "Organization ID or name"
// @Param workflow path string true "Workflow name"
// @Param request body codersdk.UpdateOrganizationSkillACLRequest true "Request body"
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/workflows/{workflow}/acl [patch]
// @x-apidocgen {"skip": true}
func (api *API) patchForkOrganizationSavedWorkflowACL(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	current, ok := api.readForkOrganizationSavedWorkflowForShare(rw, r)
	if !ok {
		return
	}
	var req codersdk.UpdateOrganizationSkillACLRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	err := forkdb.InTx(api.Database, func(tx database.Store, ftx forkdb.Store) error {
		// Lock granted users' memberships before the workflow row, in the
		// same order as a member removal and its trigger, so a removal cannot
		// miss the grant written below.
		if userIDs := activeACLIDs(req.UserRoles); len(userIDs) > 0 {
			//nolint:gocritic // Validation below reports users that are not members.
			_, err := tx.LockOrganizationMembersByUserIDsForShare(dbauthz.AsSystemRestricted(ctx), database.LockOrganizationMembersByUserIDsForShareParams{
				OrganizationID: current.OrganizationID,
				UserIds:        userIDs,
			})
			if err != nil {
				return xerrors.Errorf("lock organization members: %w", err)
			}
		}
		//nolint:gocritic // The ACL write below reauthorizes the locked row for share.
		locked, err := ftx.GetOrganizationSavedWorkflowByNameForUpdate(dbauthz.AsSystemRestricted(ctx), forkdb.GetOrganizationSavedWorkflowByNameForUpdateParams{
			OrganizationID: current.OrganizationID,
			Name:           current.Name,
		})
		if err != nil {
			return xerrors.Errorf("get organization saved workflow for update: %w", err)
		}
		userRoles, groupRoles, validations := validateOrganizationACLUpdate(ctx, tx, current.OrganizationID, organizationSkillACLUpdateValidator(req))
		if len(validations) > 0 {
			return &organizationSkillACLValidationError{validations: validations}
		}
		userACL := maps.Clone(locked.UserAcl)
		groupACL := maps.Clone(locked.GroupAcl)
		applyACLReadRoles(userACL, userRoles)
		applyACLReadRoles(groupACL, groupRoles)
		_, err = ftx.UpdateOrganizationSavedWorkflowACLByName(ctx, forkdb.UpdateOrganizationSavedWorkflowACLByNameParams{
			GroupAcl:       groupACL,
			UserAcl:        userACL,
			OrganizationID: current.OrganizationID,
			Name:           current.Name,
		})
		if err != nil {
			return xerrors.Errorf("update organization saved workflow ACL: %w", err)
		}
		return nil
	}, nil)
	if err != nil {
		var validationErr *organizationSkillACLValidationError
		if errors.As(err, &validationErr) {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message:     "Invalid request to update organization saved workflow ACL.",
				Validations: validationErr.validations,
			})
			return
		}
		writeForkSavedWorkflowWriteError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// readForkOrganizationSavedWorkflow reads the organization workflow named in
// the path. Updates read it first so that a caller who cannot read the
// workflow gets 404, and only readers get 403.
func (api *API) readForkOrganizationSavedWorkflow(rw http.ResponseWriter, r *http.Request) (forkdb.SavedWorkflow, bool) {
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return forkdb.SavedWorkflow{}, false
	}
	row, err := fdb.GetOrganizationSavedWorkflowByName(r.Context(), forkdb.GetOrganizationSavedWorkflowByNameParams{
		OrganizationID: httpmw.OrganizationParam(r).ID,
		Name:           chi.URLParam(r, "workflow"),
	})
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return forkdb.SavedWorkflow{}, false
	}
	return row, true
}

// readForkOrganizationSavedWorkflowForShare reads the organization workflow
// named in the path for the ACL routes. It admits only callers who can share
// the workflow, so readers cannot list its grants. Everyone else gets 404.
func (api *API) readForkOrganizationSavedWorkflowForShare(rw http.ResponseWriter, r *http.Request) (forkdb.SavedWorkflow, bool) {
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return forkdb.SavedWorkflow{}, false
	}
	//nolint:gocritic // The share check below owns authorization.
	row, err := fdb.GetOrganizationSavedWorkflowByName(dbauthz.AsSystemRestricted(r.Context()), forkdb.GetOrganizationSavedWorkflowByNameParams{
		OrganizationID: httpmw.OrganizationParam(r).ID,
		Name:           chi.URLParam(r, "workflow"),
	})
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return forkdb.SavedWorkflow{}, false
	}
	if !api.Authorize(r, policy.ActionShare, row) {
		httpapi.ResourceNotFound(rw)
		return forkdb.SavedWorkflow{}, false
	}
	return row, true
}
