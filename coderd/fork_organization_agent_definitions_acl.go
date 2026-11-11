package coderd

// This file is fork code: it serves the ACLs of the organization-tier agent
// definitions of the fork-agent-definitions experiment. They reuse the
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

// @Summary Get an organization agent definition ACL
// @ID get-an-organization-agent-definition-acl
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param agentDefinition path string true "Agent definition name"
// @Success 200 {object} codersdk.OrganizationSkillACL
// @Router /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition}/acl [get]
// @x-apidocgen {"skip": true}
func (api *API) forkOrganizationAgentDefinitionACL(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, ok := api.readForkOrganizationAgentDefinitionForShare(rw, r)
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

// @Summary Get available organization agent definition ACL users and groups
// @ID get-available-organization-agent-definition-acl-users-and-groups
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param agentDefinition path string true "Agent definition name"
// @Param q query string false "User search query; free-text search also applies to groups"
// @Param after_id query string false "User after ID" format(uuid)
// @Param limit query int false "Page limit for users and groups, if 0 returns all candidates"
// @Param offset query int false "User page offset"
// @Success 200 {object} codersdk.ACLAvailable
// @Router /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition}/acl/available [get]
// @x-apidocgen {"skip": true}
func (api *API) forkOrganizationAgentDefinitionACLAvailable(rw http.ResponseWriter, r *http.Request) {
	row, ok := api.readForkOrganizationAgentDefinitionForShare(rw, r)
	if !ok {
		return
	}
	api.writeOrganizationACLAvailable(rw, r, row.OrganizationID)
}

// @Summary Update an organization agent definition ACL
// @ID update-an-organization-agent-definition-acl
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Param organization path string true "Organization ID or name"
// @Param agentDefinition path string true "Agent definition name"
// @Param request body codersdk.UpdateOrganizationSkillACLRequest true "Request body"
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/agent-definitions/{agentDefinition}/acl [patch]
// @x-apidocgen {"skip": true}
func (api *API) patchForkOrganizationAgentDefinitionACL(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	current, ok := api.readForkOrganizationAgentDefinitionForShare(rw, r)
	if !ok {
		return
	}
	var req codersdk.UpdateOrganizationSkillACLRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	err := forkdb.InTx(api.Database, func(tx database.Store, ftx forkdb.Store) error {
		// Lock granted users' memberships before the definition row, in the
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
		locked, err := ftx.GetOrganizationAgentDefinitionByNameForUpdate(dbauthz.AsSystemRestricted(ctx), forkdb.GetOrganizationAgentDefinitionByNameForUpdateParams{
			OrganizationID: current.OrganizationID,
			Name:           current.Name,
		})
		if err != nil {
			return xerrors.Errorf("get organization agent definition for update: %w", err)
		}
		userRoles, groupRoles, validations := validateOrganizationACLUpdate(ctx, tx, current.OrganizationID, organizationSkillACLUpdateValidator(req))
		if len(validations) > 0 {
			return &organizationSkillACLValidationError{validations: validations}
		}
		userACL := maps.Clone(locked.UserAcl)
		groupACL := maps.Clone(locked.GroupAcl)
		applyACLReadRoles(userACL, userRoles)
		applyACLReadRoles(groupACL, groupRoles)
		_, err = ftx.UpdateOrganizationAgentDefinitionACLByName(ctx, forkdb.UpdateOrganizationAgentDefinitionACLByNameParams{
			GroupAcl:       groupACL,
			UserAcl:        userACL,
			OrganizationID: current.OrganizationID,
			Name:           current.Name,
		})
		if err != nil {
			return xerrors.Errorf("update organization agent definition ACL: %w", err)
		}
		return nil
	}, nil)
	if err != nil {
		var validationErr *organizationSkillACLValidationError
		if errors.As(err, &validationErr) {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message:     "Invalid request to update organization agent definition ACL.",
				Validations: validationErr.validations,
			})
			return
		}
		writeForkAgentDefinitionWriteError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// readForkOrganizationAgentDefinitionForShare reads the organization
// definition named in the path for the ACL routes. Like the organization skill
// ACL routes, it admits only callers who can share the definition, so readers
// cannot list its grants and share-only API key scopes work. Everyone else
// gets 404.
func (api *API) readForkOrganizationAgentDefinitionForShare(rw http.ResponseWriter, r *http.Request) (forkdb.AgentDefinition, bool) {
	fdb, ok := forkAgentDefinitionStore(rw, api.Database)
	if !ok {
		return forkdb.AgentDefinition{}, false
	}
	//nolint:gocritic // The share check below owns authorization.
	row, err := fdb.GetOrganizationAgentDefinitionByName(dbauthz.AsSystemRestricted(r.Context()), forkdb.GetOrganizationAgentDefinitionByNameParams{
		OrganizationID: httpmw.OrganizationParam(r).ID,
		Name:           chi.URLParam(r, "agentDefinition"),
	})
	if err != nil {
		writeForkAgentDefinitionReadError(rw, err)
		return forkdb.AgentDefinition{}, false
	}
	if !api.Authorize(r, policy.ActionShare, row) {
		httpapi.ResourceNotFound(rw)
		return forkdb.AgentDefinition{}, false
	}
	return row, true
}
