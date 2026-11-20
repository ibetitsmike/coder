package coderd

// This file is fork code: it lets a member share a personal saved workflow of
// the fork-workflows experiment with the whole organization.

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac/policy"
)

// @Summary Share a user saved workflow with the organization
// @ID share-a-user-saved-workflow-with-the-organization
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param user path string true "User ID, username, or me"
// @Param workflow path string true "Workflow name"
// @Param expect_id query string false "Expected workflow ID, as a UUID" format(uuid)
// @Failure 400 {object} codersdk.Response
// @Failure 409 {object} codersdk.Response
// @Success 201 {object} codersdk.ForkSavedWorkflow
// @Router /api/v2/chats/fork/organizations/{organization}/members/{user}/workflows/{workflow}/share [post]
// @x-apidocgen {"skip": true}
func (api *API) postForkShareUserSavedWorkflow(rw http.ResponseWriter, r *http.Request) {
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
	var shared forkdb.SavedWorkflow
	err := forkdb.InTx(api.Database, func(tx database.Store, ftx forkdb.Store) error {
		// Lock the owner's membership before the workflow rows, in the same
		// order as a member removal and its trigger, so a removal cannot miss
		// the owner's grant that the move writes.
		//nolint:gocritic // The move below authorizes the caller.
		locked, err := tx.LockOrganizationMembersByUserIDsForShare(dbauthz.AsSystemRestricted(ctx), database.LockOrganizationMembersByUserIDsForShareParams{
			OrganizationID: member.OrganizationID,
			UserIds:        []uuid.UUID{member.UserID},
		})
		if err != nil {
			return xerrors.Errorf("lock organization member: %w", err)
		}
		if len(locked) == 0 {
			return sql.ErrNoRows
		}
		shared, err = ftx.ShareUserSavedWorkflowByName(ctx, forkdb.ShareUserSavedWorkflowByNameParams{
			OrganizationID: member.OrganizationID,
			UserID:         member.UserID,
			Name:           name,
			ExpectID:       expectID,
		})
		return err
	}, nil)
	probe := func(ctx context.Context) (uuid.UUID, error) {
		row, err := fdb.GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{
			OrganizationID: member.OrganizationID,
			UserID:         member.UserID,
			Name:           name,
		})
		return row.ID, err
	}
	conflict := api.forkUserSavedWorkflowConflictProbe(r, member.OrganizationID, member.UserID, name, policy.ActionDelete)
	// A taken organization name or a full organization reaches the write
	// error mapping as a 409, after the move rolled back.
	if !okAfterConditionalWrite(ctx, rw, err, expectID, false, probe, conflict) {
		return
	}
	httpapi.Write(ctx, rw, http.StatusCreated, convertForkSavedWorkflow(shared))
}
