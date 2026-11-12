package coderd

// This file is fork code: the run API of the fork-workflows experiment. The
// handlers read the run journal of a chat and stop a run; the engine and the
// journal live in coderd/x/chatd/fork_workflow*.go. registerForkChatRoutes
// mounts these handlers behind the fork-workflows experiment.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
)

const (
	// workflowRunsDefaultLimit is the page size of the run list.
	workflowRunsDefaultLimit = 50
	// workflowRunsMaxLimit bounds one page of the run list.
	workflowRunsMaxLimit = 100
	// workflowScriptPreviewBytes is how much of a script a list item keeps.
	workflowScriptPreviewBytes = 200
)

// @Summary Get the workflow runs of a chat
// @ID get-the-workflow-runs-of-a-chat
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param limit query int false "Page size, at most 100" default(50)
// @Param after query string false "Cursor: the created_at (RFC3339) and the ID of the last run of the previous page, as <RFC3339>/<run ID>"
// @Success 200 {array} codersdk.WorkflowRun
// @Failure 400 {object} codersdk.Response
// @Failure 403 {object} codersdk.Response
// @Router /api/v2/chats/fork/{chat}/workflow-runs [get]
// @x-apidocgen {"skip": true}
// @Description Lists the workflow runs of the chat, newest first, one page of
// @Description at most 100 runs. Only deployment owners of the chat owner see
// @Description runs, the same gate as the workflow tools. The list items
// @Description carry a script preview and step counts; the detail has the
// @Description full script, the steps and the result.
func (api *API) getChatWorkflowRuns(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)
	if !api.checkForkWorkflowOwner(rw, r, chat) {
		return
	}

	parser := httpapi.NewQueryParamParser()
	limit := int(parser.PositiveInt64(r.URL.Query(), workflowRunsDefaultLimit, "limit"))
	if limit > workflowRunsMaxLimit {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "limit must be at most 100.",
		})
		return
	}
	var afterCreatedAt sql.NullTime
	var afterID uuid.NullUUID
	if raw := r.URL.Query().Get("after"); raw != "" {
		createdAtText, idText, found := strings.Cut(raw, "/")
		if !found {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message: "after must be <RFC3339 timestamp>/<run ID>.",
			})
			return
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdAtText)
		if err != nil {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message: "after must be <RFC3339 timestamp>/<run ID>.",
				Detail:  "the timestamp does not parse: " + err.Error(),
			})
			return
		}
		id, err := uuid.Parse(idText)
		if err != nil {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message: "after must be <RFC3339 timestamp>/<run ID>.",
				Detail:  "the run ID does not parse: " + err.Error(),
			})
			return
		}
		afterCreatedAt = sql.NullTime{Time: createdAt, Valid: true}
		afterID = uuid.NullUUID{UUID: id, Valid: true}
	}
	if len(parser.Errors) > 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message:     "Query parameters have invalid values.",
			Validations: parser.Errors,
		})
		return
	}

	runs, counts, err := api.readChatWorkflowRuns(ctx, chat.ID, forkdb.GetWorkflowRunsByChatParams{
		ChatID:         chat.ID,
		AfterCreatedAt: afterCreatedAt,
		AfterID:        afterID,
		RowLimit:       int32(limit),
	})
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to read the workflow runs.",
			Detail:  err.Error(),
		})
		return
	}
	items := make([]codersdk.WorkflowRun, 0, len(runs))
	for _, row := range runs {
		items = append(items, workflowRunItem(row, counts[row.ID]))
	}
	httpapi.Write(ctx, rw, http.StatusOK, items)
}

// @Summary Get a workflow run of a chat
// @ID get-a-workflow-run-of-a-chat
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param workflowrun path string true "Workflow run ID" format(uuid)
// @Success 200 {object} codersdk.WorkflowRunDetail
// @Failure 400 {object} codersdk.Response
// @Failure 403 {object} codersdk.Response
// @Failure 404 {object} codersdk.Response
// @Router /api/v2/chats/fork/{chat}/workflow-runs/{workflowrun} [get]
// @x-apidocgen {"skip": true}
// @Description Returns one run with its script, its steps and, when the run
// @Description ended, its result and the log lines of the terminal snapshot.
// @Description A live run has no logs: the journal keeps no live log lines.
func (api *API) getChatWorkflowRun(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)
	if !api.checkForkWorkflowOwner(rw, r, chat) {
		return
	}
	runID, ok := httpmw.ParseUUIDParam(rw, r, "workflowrun")
	if !ok {
		return
	}

	detail, err := api.readChatWorkflowRun(ctx, chat.ID, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
				Message: "Workflow run not found.",
			})
			return
		}
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to read the workflow run.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, detail)
}

// @Summary Stop a workflow run of a chat
// @ID stop-a-workflow-run-of-a-chat
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param workflowrun path string true "Workflow run ID" format(uuid)
// @Success 200 {object} codersdk.WorkflowRunDetail
// @Failure 403 {object} codersdk.Response
// @Failure 404 {object} codersdk.Response
// @Failure 409 {object} codersdk.Response
// @Router /api/v2/chats/fork/{chat}/workflow-runs/{workflowrun}/stop [post]
// @x-apidocgen {"skip": true}
// @Description Stops a running run: the script unwinds, the open steps fail
// @Description with "stopped by user", their child chats stop, and the run
// @Description reads stopped afterwards. A run that already ended answers
// @Description 409; an interrupted run is not stoppable either, because the
// @Description agent continues it with resume_run_id.
func (api *API) postStopChatWorkflowRun(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)
	if !api.checkForkWorkflowOwner(rw, r, chat) {
		return
	}
	runID, ok := httpmw.ParseUUIDParam(rw, r, "workflowrun")
	if !ok {
		return
	}

	// The stop mutates the chat's journal state, so it authorizes as a chat
	// update first, like the other per-chat writes.
	if maybeWriteChatError(ctx, rw, api.checkUpdateChat(ctx, chat)) {
		return
	}

	err := api.chatDaemon.ForkStopWorkflowRun(ctx, chat.ID, runID)
	switch {
	case err == nil:
	case errors.Is(err, chatd.ErrForkWorkflowRunNotFound):
		httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
			Message: "Workflow run not found.",
		})
		return
	case errors.Is(err, chatd.ErrForkWorkflowRunTerminal):
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "The workflow run already ended. A stopped or completed run cannot stop again, and an interrupted run resumes by asking the agent.",
		})
		return
	default:
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to stop the workflow run.",
			Detail:  err.Error(),
		})
		return
	}

	detail, err := api.readChatWorkflowRun(ctx, chat.ID, runID)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Stopped the workflow run, but failed to read its snapshot.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, detail)
}

// checkForkWorkflowOwner is the API form of the workflow tool gate: only a
// deployment owner may read or stop the workflow runs of a chat, the same
// gate that lets the owner's turns run workflows. It fails closed.
func (api *API) checkForkWorkflowOwner(rw http.ResponseWriter, r *http.Request, chat database.Chat) bool {
	ctx := r.Context()
	owner, err := api.Database.GetUserByID(ctx, chat.OwnerID)
	if err != nil {
		api.Logger.Warn(ctx, "read the chat owner for the workflow run gate",
			slog.F("chat_id", chat.ID), slog.Error(err))
		httpapi.Write(ctx, rw, http.StatusForbidden, codersdk.Response{
			Message: "Workflow runs are only available to deployment owners.",
		})
		return false
	}
	if !slices.Contains(owner.RBACRoles, rbac.RoleOwner().String()) {
		httpapi.Write(ctx, rw, http.StatusForbidden, codersdk.Response{
			Message: "Workflow runs are only available to deployment owners.",
		})
		return false
	}
	return true
}

// readChatWorkflowRuns reads one page of a chat's run journal with the step
// counts of its runs.
func (api *API) readChatWorkflowRuns(
	ctx context.Context,
	chatID uuid.UUID,
	params forkdb.GetWorkflowRunsByChatParams,
) ([]forkdb.WorkflowRun, map[uuid.UUID]codersdk.WorkflowRunStepCounts, error) {
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		return nil, nil, err
	}
	runs, err := fdb.GetWorkflowRunsByChat(ctx, params)
	if err != nil {
		return nil, nil, err
	}
	countRows, err := fdb.GetWorkflowRunStepCountsByChat(ctx, chatID)
	if err != nil {
		return nil, nil, err
	}
	counts := make(map[uuid.UUID]codersdk.WorkflowRunStepCounts, len(runs))
	for _, row := range countRows {
		count := counts[row.RunID]
		count.Total += int(row.StepCount)
		switch row.Status {
		case "reported":
			count.Done += int(row.StepCount)
		case "failed":
			count.Failed += int(row.StepCount)
		default: // running or reminded: in flight after a possible nudge
			count.Running += int(row.StepCount)
		}
		counts[row.RunID] = count
	}
	return runs, counts, nil
}

// workflowRunItem builds one list item from a journal row and its counts.
func workflowRunItem(row forkdb.WorkflowRun, counts codersdk.WorkflowRunStepCounts) codersdk.WorkflowRun {
	item := codersdk.WorkflowRun{
		ID:            row.ID.String(),
		ChatID:        row.ChatID.String(),
		Status:        row.Status,
		Error:         row.Error,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		Title:         "",
		ScriptPreview: workflowScriptPreview(row.Script),
		StepCounts:    counts,
	}
	if row.FinishedAt.Valid {
		finishedAt := row.FinishedAt.Time
		item.FinishedAt = &finishedAt
	}
	return item
}

// workflowScriptPreview cuts the script to at most workflowScriptPreviewBytes
// on a rune boundary.
func workflowScriptPreview(script string) string {
	if len(script) <= workflowScriptPreviewBytes {
		return script
	}
	cut := workflowScriptPreviewBytes
	for cut > 0 && !utf8.RuneStart(script[cut]) {
		cut--
	}
	return script[:cut]
}

// readChatWorkflowRun builds the detail of one run from its journal rows.
// The terminal snapshot keeps the result and the log lines; a live run has
// neither.
func (api *API) readChatWorkflowRun(ctx context.Context, chatID, runID uuid.UUID) (codersdk.WorkflowRunDetail, error) {
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		return codersdk.WorkflowRunDetail{}, err
	}
	row, err := fdb.GetWorkflowRun(ctx, runID)
	if err != nil {
		return codersdk.WorkflowRunDetail{}, err
	}
	if row.ChatID != chatID {
		return codersdk.WorkflowRunDetail{}, sql.ErrNoRows
	}
	steps, err := fdb.GetWorkflowStepsByRun(ctx, runID)
	if err != nil {
		return codersdk.WorkflowRunDetail{}, err
	}

	detail := codersdk.WorkflowRunDetail{
		ID:        row.ID.String(),
		ChatID:    row.ChatID.String(),
		Status:    row.Status,
		Error:     row.Error,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Title:     "",
		Script:    row.Script,
		Logs:      []string{},
		Steps:     make([]codersdk.WorkflowRunStep, 0, len(steps)),
	}
	if row.FinishedAt.Valid {
		finishedAt := row.FinishedAt.Time
		detail.FinishedAt = &finishedAt
	}
	// The snapshot of a terminal run keeps the script's result and its last
	// log lines.
	if row.Result.Valid {
		var snapshot struct {
			Result json.RawMessage `json:"result"`
			Logs   []string        `json:"logs"`
		}
		if err := json.Unmarshal(row.Result.RawMessage, &snapshot); err != nil {
			return codersdk.WorkflowRunDetail{}, err
		}
		detail.Result = snapshot.Result
		detail.Logs = snapshot.Logs
		if detail.Logs == nil {
			detail.Logs = []string{}
		}
	}
	for _, step := range steps {
		sdkStep := codersdk.WorkflowRunStep{
			ID:        step.ID.String(),
			RunID:     step.RunID.String(),
			Title:     step.Title,
			Status:    step.Status,
			Reused:    false,
			Error:     step.Error,
			Ordinal:   int(step.Ordinal),
			StartedAt: step.StartedAt,
		}
		if step.Value.Valid {
			sdkStep.Value = json.RawMessage(step.Value.RawMessage)
		}
		if step.FinishedAt.Valid {
			finishedAt := step.FinishedAt.Time
			sdkStep.FinishedAt = &finishedAt
		}
		detail.Steps = append(detail.Steps, sdkStep)
	}
	return detail, nil
}
