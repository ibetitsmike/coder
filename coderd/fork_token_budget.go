package coderd

// This file is fork code: it serves the organization compaction setting of
// the fork-token-budget experiment.

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"

	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary Get organization token budget setting
// @ID get-organization-token-budget-setting
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID" format(uuid)
// @Success 200 {object} codersdk.ForkOrganizationTokenBudget
// @Router /api/v2/chats/fork/organizations/{organization}/token-budget [get]
func (api *API) forkOrganizationTokenBudget(rw http.ResponseWriter, r *http.Request) {
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
	row, err := fdb.GetChatOrganizationTokenBudget(ctx, organization.ID)
	if errors.Is(err, sql.ErrNoRows) {
		httpapi.Write(ctx, rw, http.StatusOK, codersdk.DefaultForkOrganizationTokenBudget())
		return
	}
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Internal error fetching organization token budget setting.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkOrganizationTokenBudget(row))
}

// @Summary Update organization token budget setting
// @ID update-organization-token-budget-setting
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID" format(uuid)
// @Param request body codersdk.ForkOrganizationTokenBudget true "Request body"
// @Success 200 {object} codersdk.ForkOrganizationTokenBudget
// @Router /api/v2/chats/fork/organizations/{organization}/token-budget [put]
func (api *API) updateForkOrganizationTokenBudget(rw http.ResponseWriter, r *http.Request) {
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

	var req codersdk.ForkOrganizationTokenBudget
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	var validations []codersdk.ValidationError
	if !slices.Contains(codersdk.ForkTokenBudgetStrategies, req.Strategy) {
		validations = append(validations, codersdk.ValidationError{Field: "strategy", Detail: `Must be "summary" or "token_budget".`})
	}
	if !slices.Contains(codersdk.ForkTokenBudgetFallbacks, req.Fallback) {
		validations = append(validations, codersdk.ValidationError{Field: "fallback", Detail: `Must be "new_window" or "summary".`})
	}
	if len(validations) > 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message:     "Invalid token budget setting.",
			Validations: validations,
		})
		return
	}

	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	row, err := fdb.UpsertChatOrganizationTokenBudget(ctx, forkdb.UpsertChatOrganizationTokenBudgetParams{
		OrganizationID: organization.ID,
		Strategy:       string(req.Strategy),
		Fallback:       string(req.Fallback),
	})
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Internal error updating organization token budget setting.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkOrganizationTokenBudget(row))
}

func convertForkOrganizationTokenBudget(row forkdb.ChatOrganizationTokenBudget) codersdk.ForkOrganizationTokenBudget {
	return codersdk.ForkOrganizationTokenBudget{
		Strategy: codersdk.ForkTokenBudgetStrategy(row.Strategy),
		Fallback: codersdk.ForkTokenBudgetFallback(row.Fallback),
	}
}
