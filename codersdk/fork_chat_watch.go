package codersdk

// This file is fork code: it holds the watch event that tells a client to
// refetch the fork panels of a chat, so upstream's chats.go keeps only the
// one field that carries it.

import "github.com/google/uuid"

// ChatWatchEventKindForkChange tells the client that state behind a fork
// panel of a chat changed. Its Chat is empty; take the chat from ForkChange.
const ChatWatchEventKindForkChange ChatWatchEventKind = "fork_change"

// ChatWatchForkFacet names the fork state that a fork_change event reports.
type ChatWatchForkFacet string

const (
	// ChatWatchForkFacetQuestions: a descendant of the family asked a
	// question.
	ChatWatchForkFacetQuestions ChatWatchForkFacet = "questions"
	// ChatWatchForkFacetWakeups: a wake-up of the chat was created, paused,
	// resumed, canceled or ran.
	ChatWatchForkFacetWakeups ChatWatchForkFacet = "wakeups"
	// ChatWatchForkFacetWorkflows: a workflow run of the chat, or one of its
	// steps, changed.
	ChatWatchForkFacetWorkflows ChatWatchForkFacet = "workflows"
	// ChatWatchForkFacetWorkflowLogs: only the live log lines of a workflow
	// run of the chat changed.
	ChatWatchForkFacetWorkflowLogs ChatWatchForkFacet = "workflow_logs"
)

// ChatWatchForkChange names the chat and the fork state that changed, so
// the client refetches only that.
type ChatWatchForkChange struct {
	// ChatID is the chat whose state changed: the chat that asked the
	// question, owns the wake-up or ran the workflow.
	ChatID uuid.UUID `json:"chat_id" format:"uuid"`
	// RootChatID is the root of ChatID's family, ChatID itself for a root.
	RootChatID uuid.UUID            `json:"root_chat_id" format:"uuid"`
	Facets     []ChatWatchForkFacet `json:"facets"`
	// WorkflowRunID is set for the workflow facets.
	WorkflowRunID *uuid.UUID `json:"workflow_run_id,omitempty" format:"uuid"`
}
