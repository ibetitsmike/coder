package codersdk

// This file is fork code: the run API of the fork-workflows experiment. The
// routes live in coderd/fork_workflow_runs.go; the engine and its journal
// live in coderd/x/chatd/fork_workflow*.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// WorkflowRunStatus is the status of a workflow run. running: a replica
// executes the script. interrupted: a restart marked the run before any
// resume; the agent continues it with resume_run_id. completed, failed and
// stopped are terminal.
type WorkflowRunStatus string

const (
	WorkflowRunStatusRunning     WorkflowRunStatus = "running"
	WorkflowRunStatusInterrupted WorkflowRunStatus = "interrupted"
	WorkflowRunStatusCompleted   WorkflowRunStatus = "completed"
	WorkflowRunStatusFailed      WorkflowRunStatus = "failed"
	WorkflowRunStatusStopped     WorkflowRunStatus = "stopped"
)

// WorkflowStepStatus is the status of one step of a run. reminded means the
// step got its one retry nudge and is still in flight.
type WorkflowStepStatus string

const (
	WorkflowStepStatusRunning  WorkflowStepStatus = "running"
	WorkflowStepStatusReminded WorkflowStepStatus = "reminded"
	WorkflowStepStatusReported WorkflowStepStatus = "reported"
	WorkflowStepStatusFailed   WorkflowStepStatus = "failed"
)

// WorkflowRunStepCounts summarizes the steps of a run for a list item.
// Done counts reported steps; running counts running and reminded steps.
type WorkflowRunStepCounts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
}

// WorkflowRun is one item of a chat's workflow run list. It carries a
// preview of the script instead of the script itself; the full script is on
// the detail.
type WorkflowRun struct {
	ID        string    `json:"id" format:"uuid"`
	ChatID    string    `json:"chat_id" format:"uuid"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at" format:"date-time"`
	UpdatedAt time.Time `json:"updated_at" format:"date-time"`
	// FinishedAt is the end of a terminal run. An interrupted run is open,
	// so it has none until a resume ends it.
	FinishedAt *time.Time `json:"finished_at,omitempty" format:"date-time"`
	// Title is a display name for the run. Runs have no title yet, so it is
	// empty and the UI shows the script preview.
	Title         string `json:"title"`
	ScriptPreview string `json:"script_preview,omitempty"`
	// Phases lists the run's phases in display order: the script's declared
	// PHASES list first, then every undeclared name that phase() reached.
	// CurrentPhase is the one the script set last. Both are empty for a run
	// whose script uses no phases.
	Phases       []string              `json:"phases"`
	CurrentPhase string                `json:"current_phase"`
	StepCounts   WorkflowRunStepCounts `json:"step_counts"`
}

// WorkflowRunStep is one step of a run. The step ID is the child chat ID.
// Reused is true when a resumed run replayed the step instead of running it
// again.
type WorkflowRunStep struct {
	ID      string `json:"id" format:"uuid"`
	RunID   string `json:"run_id" format:"uuid"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Reused  bool   `json:"reused"`
	Error   string `json:"error"`
	Ordinal int    `json:"ordinal"`
	// Value is the report the step sent, matching its schema. It is empty
	// until the step reported.
	Value json.RawMessage `json:"value,omitempty"`
	// Phase is the phase that was current when the step started, empty
	// before the script's first phase() call.
	Phase string `json:"phase"`
	// StartedAt is when the step's child chat was created (or reused).
	StartedAt time.Time `json:"started_at" format:"date-time"`
	// FinishedAt is when the step reported or failed. An open step has none.
	FinishedAt *time.Time `json:"finished_at,omitempty" format:"date-time"`
}

// WorkflowRunDetail is the full view of one run: its script, its args, its
// steps, and for a terminal run the result snapshot. The logs are the last
// log lines of a terminal run, from its snapshot, or of a live run, written
// through to the journal as the script logs.
type WorkflowRunDetail struct {
	ID         string     `json:"id" format:"uuid"`
	ChatID     string     `json:"chat_id" format:"uuid"`
	Status     string     `json:"status"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"created_at" format:"date-time"`
	UpdatedAt  time.Time  `json:"updated_at" format:"date-time"`
	FinishedAt *time.Time `json:"finished_at,omitempty" format:"date-time"`
	Title      string     `json:"title"`
	Script     string     `json:"script"`
	// Args is the JSON value that main(args) received. It is usually an
	// object, but an inline script may be given any JSON value. The API
	// returns the args bytes the run was started with, so a client that
	// reads them through json.RawMessage keeps them exact.
	Args any `json:"args"`
	// Result is the value that main returned, for a terminal run. The API
	// returns the result bytes main encoded, so a client that reads them
	// through json.RawMessage keeps them exact.
	Result any `json:"result,omitempty"`
	// Logs are the last log lines the script printed: from the snapshot of
	// a terminal run, or written through to the journal while it runs.
	Logs []string `json:"logs"`
	// Phases and CurrentPhase carry the run's phase rail, as on the list
	// item.
	Phases       []string          `json:"phases"`
	CurrentPhase string            `json:"current_phase"`
	Steps        []WorkflowRunStep `json:"steps"`
}

// UnmarshalJSON decodes Args and Result through json.RawMessage, so a
// number outside the float64 exact range, for example the Starlark big
// integer 9007199254740993, survives the SDK round trip instead of
// rounding. The wire values are JSON, and the fields of the alias type
// shadow the any fields: a present key keeps its raw bytes, an absent key
// stays nil, and the result lands in the interface field only when the
// snapshot carried one.
func (d *WorkflowRunDetail) UnmarshalJSON(data []byte) error {
	type workflowRunDetailAlias WorkflowRunDetail
	var alias struct {
		workflowRunDetailAlias
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result,omitempty"`
	}
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*d = WorkflowRunDetail(alias.workflowRunDetailAlias)
	if len(alias.Args) > 0 {
		d.Args = alias.Args
	}
	if len(alias.Result) > 0 {
		d.Result = alias.Result
	}
	return nil
}

// WorkflowRunListOptions is the pagination of a chat's workflow run list.
type WorkflowRunListOptions struct {
	// After is the cursor of the previous page:
	// <created_at RFC3339>/<run ID>. Empty reads the first page.
	After string `json:"-"`
	// Limit is the page size, at most 100.
	Limit int `json:"-"`
}

// GetChatWorkflowRuns lists the workflow runs of a chat, newest first.
func (c *Client) GetChatWorkflowRuns(ctx context.Context, chatID uuid.UUID, opts WorkflowRunListOptions) ([]WorkflowRun, error) {
	var ropts []RequestOption
	if opts.After != "" {
		ropts = append(ropts, WithQueryParam("after", opts.After))
	}
	if opts.Limit > 0 {
		ropts = append(ropts, WithQueryParam("limit", strconv.Itoa(opts.Limit)))
	}
	res, err := c.Request(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/chats/fork/%s/workflow-runs", chatID), nil, ropts...)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, ReadBodyAsError(res)
	}
	var items []WorkflowRun
	return items, json.NewDecoder(res.Body).Decode(&items)
}

// GetChatWorkflowRun returns one run of a chat with its steps and, when it
// ended, its result and logs.
func (c *Client) GetChatWorkflowRun(ctx context.Context, chatID, runID uuid.UUID) (WorkflowRunDetail, error) {
	res, err := c.Request(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/chats/fork/%s/workflow-runs/%s", chatID, runID), nil)
	if err != nil {
		return WorkflowRunDetail{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return WorkflowRunDetail{}, ReadBodyAsError(res)
	}
	var detail WorkflowRunDetail
	return detail, json.NewDecoder(res.Body).Decode(&detail)
}

// StopChatWorkflowRun stops a running run and returns its snapshot. A run
// that already ended answers 409.
func (c *Client) StopChatWorkflowRun(ctx context.Context, chatID, runID uuid.UUID) error {
	res, err := c.Request(ctx, http.MethodPost,
		fmt.Sprintf("/api/v2/chats/fork/%s/workflow-runs/%s/stop", chatID, runID), nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ReadBodyAsError(res)
	}
	_, err = io.Copy(io.Discard, res.Body)
	return err
}
