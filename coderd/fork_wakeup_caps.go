package coderd

// This file is fork code: it serves the organization wake-up caps of the
// fork-chat-wakeups experiment.

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary Get organization wake-up caps
// @ID get-organization-wake-up-caps
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID" format(uuid)
// @Success 200 {object} codersdk.ForkOrganizationWakeupCapsResponse
// @Router /api/v2/chats/fork/organizations/{organization}/wakeup-caps [get]
func (api *API) forkOrganizationWakeupCaps(rw http.ResponseWriter, r *http.Request) {
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
	row, err := fdb.GetChatOrganizationWakeupCaps(ctx, organization.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Internal error fetching organization wake-up caps.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, forkOrganizationWakeupCapsResponse(row))
}

// @Summary Update organization wake-up caps
// @ID update-organization-wake-up-caps
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID" format(uuid)
// @Param request body codersdk.ForkOrganizationWakeupCaps true "Request body"
// @Success 200 {object} codersdk.ForkOrganizationWakeupCapsResponse
// @Router /api/v2/chats/fork/organizations/{organization}/wakeup-caps [put]
func (api *API) updateForkOrganizationWakeupCaps(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	object := rbac.ResourceChatModelConfig.InOrg(organization.ID)
	if !api.Authorize(r, policy.ActionUpdate, object) {
		if api.Authorize(r, policy.ActionRead, object) {
			httpapi.Forbidden(rw)
		} else {
			httpapi.ResourceNotFound(rw)
		}
		return
	}

	var req codersdk.ForkOrganizationWakeupCaps
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	if validations := validateForkOrganizationWakeupCaps(req); len(validations) > 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message:     "Invalid wake-up caps.",
			Validations: validations,
		})
		return
	}

	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	row, err := fdb.UpsertChatOrganizationWakeupCaps(ctx, forkdb.UpsertChatOrganizationWakeupCapsParams{
		OrganizationID:                   organization.ID,
		MaxChatRunsPerDay:                forkNullInt32(req.MaxChatRunsPerDay),
		MaxOrganizationRunsPerDay:        forkNullInt32(req.MaxOrganizationRunsPerDay),
		MaxChatSpendMicrosPerDay:         forkNullInt64(req.MaxChatSpendMicrosPerDay),
		MaxOrganizationSpendMicrosPerDay: forkNullInt64(req.MaxOrganizationSpendMicrosPerDay),
	})
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Internal error updating organization wake-up caps.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, forkOrganizationWakeupCapsResponse(row))
}

func validateForkOrganizationWakeupCaps(req codersdk.ForkOrganizationWakeupCaps) []codersdk.ValidationError {
	var validations []codersdk.ValidationError
	for _, limit := range []struct {
		field   string
		invalid bool
	}{
		{"max_chat_runs_per_day", req.MaxChatRunsPerDay != nil && *req.MaxChatRunsPerDay < 1},
		{"max_organization_runs_per_day", req.MaxOrganizationRunsPerDay != nil && *req.MaxOrganizationRunsPerDay < 1},
		{"max_chat_spend_micros_per_day", req.MaxChatSpendMicrosPerDay != nil && *req.MaxChatSpendMicrosPerDay < 1},
		{"max_organization_spend_micros_per_day", req.MaxOrganizationSpendMicrosPerDay != nil && *req.MaxOrganizationSpendMicrosPerDay < 1},
	} {
		if limit.invalid {
			validations = append(validations, codersdk.ValidationError{Field: limit.field, Detail: "Must be at least 1."})
		}
	}
	return validations
}

func forkOrganizationWakeupCapsResponse(row forkdb.ChatOrganizationWakeupCap) codersdk.ForkOrganizationWakeupCapsResponse {
	return codersdk.ForkOrganizationWakeupCapsResponse{
		Organization: codersdk.ForkOrganizationWakeupCaps{
			MaxChatRunsPerDay:                forkInt32Ptr(row.MaxChatRunsPerDay),
			MaxOrganizationRunsPerDay:        forkInt32Ptr(row.MaxOrganizationRunsPerDay),
			MaxChatSpendMicrosPerDay:         forkInt64Ptr(row.MaxChatSpendMicrosPerDay),
			MaxOrganizationSpendMicrosPerDay: forkInt64Ptr(row.MaxOrganizationSpendMicrosPerDay),
		},
		Default:   chatd.EffectiveWakeupCaps(forkdb.ChatOrganizationWakeupCap{}),
		Effective: chatd.EffectiveWakeupCaps(row),
	}
}

func forkNullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func forkInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
