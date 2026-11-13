package codersdk

// This file is fork code: it holds the chat wake-up types and client methods
// of the fork-chat-wakeups experiment, so upstream's chats.go stays untouched.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ChatWakeupKind is how a chat wake-up schedules its runs.
type ChatWakeupKind string

// The kinds of chat wake-ups.
const (
	ChatWakeupKindDelay    ChatWakeupKind = "delay"
	ChatWakeupKindInterval ChatWakeupKind = "interval"
	ChatWakeupKindCron     ChatWakeupKind = "cron"
	ChatWakeupKindEvents   ChatWakeupKind = "events"
)

// ChatWakeupStatus is the state of a chat wake-up. Only an active wake-up
// runs; completed and canceled are final.
type ChatWakeupStatus string

// The states of a chat wake-up.
const (
	ChatWakeupStatusActive    ChatWakeupStatus = "active"
	ChatWakeupStatusPaused    ChatWakeupStatus = "paused"
	ChatWakeupStatusCompleted ChatWakeupStatus = "completed"
	ChatWakeupStatusCanceled  ChatWakeupStatus = "canceled"
)

// ChatWakeupEvent is one event that an events wake-up waits for.
type ChatWakeupEvent struct {
	// Type is child_done for a child chat that finishes, pull_request for
	// activity on a pull request, or process_exit for a background process
	// that exits.
	Type string `json:"type"`
	// ChatID is the child chat of a child_done event.
	ChatID *uuid.UUID `json:"chat_id,omitempty" format:"uuid"`
	// URL is the pull request of a pull_request event.
	URL string `json:"url,omitempty"`
	// On lists the pull request activity that wakes the chat.
	On []string `json:"on,omitempty"`
	// ProcessID is the background process of a process_exit event.
	ProcessID string `json:"process_id,omitempty"`
	// Command is the command line of that process.
	Command string `json:"command,omitempty"`
}

// ChatWakeup is a scheduled message that a chat sends itself, created by the
// schedule_wakeup or wait_for_events tool or the /loop command.
type ChatWakeup struct {
	ID             uuid.UUID      `json:"id" format:"uuid"`
	ChatID         uuid.UUID      `json:"chat_id" format:"uuid"`
	OrganizationID uuid.UUID      `json:"organization_id" format:"uuid"`
	CreatedBy      uuid.UUID      `json:"created_by" format:"uuid"`
	Source         string         `json:"source"`
	Kind           ChatWakeupKind `json:"kind"`
	Prompt         string         `json:"prompt"`
	// IntervalSeconds is set for an interval wake-up.
	IntervalSeconds *int32 `json:"interval_seconds,omitempty"`
	// Cron is set for a cron wake-up, which runs in Timezone.
	Cron     *string           `json:"cron,omitempty"`
	Timezone string            `json:"timezone"`
	Events   []ChatWakeupEvent `json:"events"`
	Status   ChatWakeupStatus  `json:"status"`
	// StatusReason tells why the wake-up paused, completed or was canceled.
	StatusReason string `json:"status_reason,omitempty"`
	// NextRunAt is when an active wake-up runs next. A paused wake-up keeps
	// the run time it had, unless the worker paused it.
	NextRunAt *time.Time `json:"next_run_at,omitempty" format:"date-time"`
	ExpiresAt *time.Time `json:"expires_at,omitempty" format:"date-time"`
	MaxRuns   *int32     `json:"max_runs,omitempty"`
	RunCount  int32      `json:"run_count"`
	LastRunAt *time.Time `json:"last_run_at,omitempty" format:"date-time"`
	CreatedAt time.Time  `json:"created_at" format:"date-time"`
	UpdatedAt time.Time  `json:"updated_at" format:"date-time"`
}

// UpdateChatWakeupRequest pauses (paused), resumes (active) or cancels
// (canceled) a chat wake-up.
type UpdateChatWakeupRequest struct {
	Status ChatWakeupStatus `json:"status" enums:"active,paused,canceled"`
}

// ChatWakeups lists the wake-ups of a chat, oldest first.
func (c *Client) ChatWakeups(ctx context.Context, chatID uuid.UUID) ([]ChatWakeup, error) {
	return requestJSON[[]ChatWakeup](ctx, c, http.MethodGet, fmt.Sprintf("/api/v2/chats/fork/%s/wakeups", chatID), http.StatusOK, nil)
}

// UpdateChatWakeup pauses, resumes or cancels a wake-up of a chat and returns
// the wake-up.
func (c *Client) UpdateChatWakeup(ctx context.Context, chatID, wakeupID uuid.UUID, req UpdateChatWakeupRequest) (ChatWakeup, error) {
	return requestJSON[ChatWakeup](ctx, c, http.MethodPatch, fmt.Sprintf("/api/v2/chats/fork/%s/wakeups/%s", chatID, wakeupID), http.StatusOK, req)
}
