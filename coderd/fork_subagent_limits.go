package coderd

// This file is fork code: it serves the organization subagent limits of the
// fork-nested-subagents experiment.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary Get organization subagent limits
// @ID get-organization-subagent-limits
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID" format(uuid)
// @Success 200 {object} codersdk.ForkOrganizationSubagentLimits
// @Router /api/v2/chats/fork/organizations/{organization}/subagent-limits [get]
func (api *API) forkOrganizationSubagentLimits(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	if !api.Authorize(r, policy.ActionRead, rbac.ResourceChatModelConfig.InOrg(organization.ID)) {
		httpapi.ResourceNotFound(rw)
		return
	}
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	row, err := fdb.GetChatOrganizationSubagentLimits(ctx, organization.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Internal error fetching organization subagent limits.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkOrganizationSubagentLimits(row))
}

// @Summary Update organization subagent limits
// @ID update-organization-subagent-limits
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID" format(uuid)
// @Param request body codersdk.ForkOrganizationSubagentLimits true "Request body"
// @Success 200 {object} codersdk.ForkOrganizationSubagentLimits
// @Router /api/v2/chats/fork/organizations/{organization}/subagent-limits [put]
func (api *API) updateForkOrganizationSubagentLimits(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	object := rbac.ResourceChatModelConfig.InOrg(organization.ID)
	if !api.Authorize(r, policy.ActionRead, object) {
		httpapi.ResourceNotFound(rw)
		return
	}
	if !api.Authorize(r, policy.ActionUpdate, object) {
		httpapi.Forbidden(rw)
		return
	}

	var req codersdk.ForkOrganizationSubagentLimits
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	if validations := validateForkOrganizationSubagentLimits(req); len(validations) > 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message:     "Invalid subagent limits.",
			Validations: validations,
		})
		return
	}

	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	row, err := fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID:         organization.ID,
		MaxDepth:               forkNullInt32(req.MaxDepth),
		MaxConcurrent:          forkNullInt32(req.MaxConcurrent),
		MaxConcurrentPerFamily: forkNullInt32(req.MaxConcurrentPerFamily),
		MaxLivePerFamily:       forkNullInt32(req.MaxLivePerFamily),
	})
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Internal error updating organization subagent limits.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkOrganizationSubagentLimits(row))
}

func validateForkOrganizationSubagentLimits(req codersdk.ForkOrganizationSubagentLimits) []codersdk.ValidationError {
	var validations []codersdk.ValidationError
	if req.MaxDepth != nil && (*req.MaxDepth < 1 || *req.MaxDepth > codersdk.MaxChatMaxSubagentDepth) {
		validations = append(validations, codersdk.ValidationError{
			Field:  "max_depth",
			Detail: fmt.Sprintf("Must be between 1 and %d.", codersdk.MaxChatMaxSubagentDepth),
		})
	}
	for _, limit := range []struct {
		field string
		value *int32
	}{
		{"max_concurrent", req.MaxConcurrent},
		{"max_concurrent_per_family", req.MaxConcurrentPerFamily},
		{"max_live_per_family", req.MaxLivePerFamily},
	} {
		if limit.value != nil && *limit.value < 1 {
			validations = append(validations, codersdk.ValidationError{Field: limit.field, Detail: "Must be at least 1."})
		}
	}
	return validations
}

func convertForkOrganizationSubagentLimits(row forkdb.ChatOrganizationSubagentLimit) codersdk.ForkOrganizationSubagentLimits {
	return codersdk.ForkOrganizationSubagentLimits{
		MaxDepth:               forkInt32Ptr(row.MaxDepth),
		MaxConcurrent:          forkInt32Ptr(row.MaxConcurrent),
		MaxConcurrentPerFamily: forkInt32Ptr(row.MaxConcurrentPerFamily),
		MaxLivePerFamily:       forkInt32Ptr(row.MaxLivePerFamily),
	}
}

func forkNullInt32(v *int32) sql.NullInt32 {
	if v == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *v, Valid: true}
}

func forkInt32Ptr(v sql.NullInt32) *int32 {
	if !v.Valid {
		return nil
	}
	return &v.Int32
}
