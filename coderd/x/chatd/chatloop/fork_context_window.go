package chatloop

// This file is fork code: the context window that a compaction of the
// fork-token-budget experiment starts. CompactionResult holds it in one field.

// ContextWindowBoundary marks a compaction that started a new context window
// from the notes of the chat instead of summarizing it. The chat_summarized
// tool result carries it as context_window, so the UI can draw a divider.
type ContextWindowBoundary struct {
	// ID is the window that starts, such as "w:2".
	ID string `json:"id"`
	// Trigger is ContextWindowTriggerNewContext or
	// ContextWindowTriggerContextLimit.
	Trigger string `json:"trigger"`
}

const (
	// ContextWindowTriggerNewContext means the model called new_context.
	ContextWindowTriggerNewContext = "new_context"
	// ContextWindowTriggerContextLimit means the prompt reached the usable
	// limit of the chat model.
	ContextWindowTriggerContextLimit = "context_limit"
)
