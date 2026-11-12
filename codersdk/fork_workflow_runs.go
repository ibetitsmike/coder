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
	Title         string                `json:"title"`
	ScriptPreview string                `json:"script_preview,omitempty"`
	StepCounts    WorkflowRunStepCounts `json:"step_counts"`
}

// WorkflowRunStep is one step of a run. The step ID is the child chat ID.
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
	// StartedAt is when the step's child chat was created (or reused).
	StartedAt time.Time `json:"started_at" format:"date-time"`
	// FinishedAt is when the step reported or failed. An open step has none.
	FinishedAt *time.Time `json:"finished_at,omitempty" format:"date-time"`
}

// WorkflowRunDetail is the full view of one run: its script, its steps, and
// for a terminal run the result snapshot and the log lines the script
// printed. A live run has empty logs: the journal keeps no live log lines,
// only the terminal snapshot.
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
	// Result is the value that main returned, for a terminal run.
	Result json.RawMessage `json:"result,omitempty"`
	// Logs are the last log lines of a terminal run, from its snapshot.
	Logs  []string          `json:"logs"`
	Steps []WorkflowRunStep `json:"steps"`
} // WorkflowRunListOptions is the pagination of a chat's workflow run list.
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
