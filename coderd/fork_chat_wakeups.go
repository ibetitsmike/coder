package coderd

// This file is fork code: it serves the chat wake-ups of the
// fork-chat-wakeups experiment. registerForkChatRoutes mounts these handlers.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/audit"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary List chat wake-ups
// @ID list-chat-wake-ups
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Success 200 {array} codersdk.ChatWakeup
// @Router /api/v2/chats/fork/{chat}/wakeups [get]
// @x-apidocgen {"skip": true}
// @Description Returns every wake-up of the chat, oldest first, including
// @Description completed and canceled ones.
func (api *API) getChatWakeups(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)

	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	rows, err := fdb.ListChatWakeupsByChatID(ctx, chat.ID)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to list chat wake-ups.",
			Detail:  err.Error(),
		})
		return
	}
	wakeups := make([]codersdk.ChatWakeup, 0, len(rows))
	for _, row := range rows {
		wakeup, err := convertChatWakeup(row)
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		wakeups = append(wakeups, wakeup)
	}
	httpapi.Write(ctx, rw, http.StatusOK, wakeups)
}

// chatWakeupAuditFields are the additional fields of the audit entry of a
// wake-up change, which is recorded as a write of its chat.
type chatWakeupAuditFields struct {
	WakeupID  uuid.UUID                 `json:"wakeup_id"`
	OldStatus codersdk.ChatWakeupStatus `json:"old_status,omitempty"`
	NewStatus codersdk.ChatWakeupStatus `json:"new_status"`
}

// @Summary Update a chat wake-up
// @ID update-a-chat-wake-up
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param wakeup path string true "Wake-up ID" format(uuid)
// @Param request body codersdk.UpdateChatWakeupRequest true "Update chat wake-up request"
// @Success 200 {object} codersdk.ChatWakeup
// @Failure 400 {object} codersdk.Response
// @Failure 404 {object} codersdk.Response
// @Failure 409 {object} codersdk.Response
// @Router /api/v2/chats/fork/{chat}/wakeups/{wakeup} [patch]
// @x-apidocgen {"skip": true}
// @Description Pauses (paused), resumes (active) or cancels (canceled) a
// @Description wake-up of the chat. Setting the status the wake-up already
// @Description has changes nothing. A resumed wake-up keeps a run time that
// @Description is still ahead, and completes when it has no run left.
func (api *API) patchChatWakeup(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)
	wakeupID, ok := httpmw.ParseUUIDParam(rw, r, "wakeup")
	if !ok {
		return
	}
	if maybeWriteChatError(ctx, rw, api.checkUpdateChat(ctx, chat)) {
		return
	}

	// Fork resources have no audit resource type, so the change is audited
	// as a write of the chat, with the wake-up in the additional fields.
	auditFields := &chatWakeupAuditFields{WakeupID: wakeupID}
	aReq, commitAudit := audit.InitRequest[database.Chat](rw, &audit.RequestParams{
		Audit:            *api.Auditor.Load(),
		Log:              api.Logger,
		Request:          r,
		Action:           database.AuditActionWrite,
		AdditionalFields: auditFields,
	})
	defer commitAudit()
	aReq.Old = chat
	aReq.New = chat
	aReq.UpdateOrganizationID(chat.OrganizationID)

	var req codersdk.UpdateChatWakeupRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	auditFields.NewStatus = req.Status
	switch req.Status {
	case codersdk.ChatWakeupStatusActive, codersdk.ChatWakeupStatusPaused, codersdk.ChatWakeupStatusCanceled:
	default:
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Invalid wake-up status.",
			Validations: []codersdk.ValidationError{{
				Field:  "status",
				Detail: `status must be "active", "paused" or "canceled"`,
			}},
		})
		return
	}
	if chat.Archived {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Cannot change the wake-ups of an archived chat.",
		})
		return
	}

	before, after, err := api.chatDaemon.SetChatWakeupStatus(ctx, chat, wakeupID, forkdb.ChatWakeupStatus(req.Status))
	switch {
	case httpapi.Is404Error(err):
		httpapi.ResourceNotFound(rw)
		return
	case errors.Is(err, chatd.ErrChatWakeupFinished):
		auditFields.OldStatus = codersdk.ChatWakeupStatus(before.Status)
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "This wake-up has already finished.",
		})
		return
	case err != nil:
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to update the chat wake-up.",
			Detail:  err.Error(),
		})
		return
	}
	auditFields.OldStatus = codersdk.ChatWakeupStatus(before.Status)
	auditFields.NewStatus = codersdk.ChatWakeupStatus(after.Status)

	wakeup, err := convertChatWakeup(after)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, wakeup)
}

// convertChatWakeup converts a stored wake-up to its API form.
func convertChatWakeup(row forkdb.ChatWakeup) (codersdk.ChatWakeup, error) {
	events := []codersdk.ChatWakeupEvent{}
	if len(row.Events) > 0 {
		if err := json.Unmarshal(row.Events, &events); err != nil {
			return codersdk.ChatWakeup{}, xerrors.Errorf("decode events of wake-up %s: %w", row.ID, err)
		}
		if events == nil {
			events = []codersdk.ChatWakeupEvent{}
		}
	}
	wakeup := codersdk.ChatWakeup{
		ID:             row.ID,
		ChatID:         row.ChatID,
		OrganizationID: row.OrganizationID,
		CreatedBy:      row.CreatedBy,
		Source:         row.Source,
		Kind:           codersdk.ChatWakeupKind(row.Kind),
		Prompt:         row.Prompt,
		Timezone:       row.Timezone,
		Events:         events,
		Status:         codersdk.ChatWakeupStatus(row.Status),
		StatusReason:   row.StatusReason.String,
		RunCount:       row.RunCount,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
	if row.IntervalSeconds.Valid {
		wakeup.IntervalSeconds = &row.IntervalSeconds.Int32
	}
	if row.Cron.Valid {
		wakeup.Cron = &row.Cron.String
	}
	if row.NextRunAt.Valid {
		wakeup.NextRunAt = &row.NextRunAt.Time
	}
	if row.ExpiresAt.Valid {
		wakeup.ExpiresAt = &row.ExpiresAt.Time
	}
	if row.MaxRuns.Valid {
		wakeup.MaxRuns = &row.MaxRuns.Int32
	}
	if row.LastRunAt.Valid {
		wakeup.LastRunAt = &row.LastRunAt.Time
	}
	return wakeup, nil
}
