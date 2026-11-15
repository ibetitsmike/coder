package chatd

// This file is fork code: it holds the context windows of the
// fork-token-budget experiment. A chat that has the notes, chat_history and
// new_context tools works in token-budget windows instead of summary compaction: it gets
// a handoff request and a final request as its window fills, saves a
// checkpoint in its notes and calls new_context, and the next window starts
// with a short lead-in instead of a summary. turn_environment.go calls
// tokenBudgetTriggers once, generation.go calls forkGenerateCompaction once,
// and fork_context.go sends the requests.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

const (
	newContextToolName = "new_context"
	// tokenBudgetMaxReserve is the most tokens a window keeps free for the
	// output of its last step. A window keeps a quarter of its limit free
	// when that is less.
	tokenBudgetMaxReserve = int64(8192)
	// tokenBudgetFinalGap is how far below the usable limit the final
	// request goes, so the model has room to save a checkpoint after it.
	tokenBudgetFinalGap = int64(10192)
	// tokenBudgetMinThresholdPercent is the lowest compression threshold the
	// handoff point follows. A lower threshold counts as this one.
	tokenBudgetMinThresholdPercent = int32(10)
	// leadInBytesPerToken estimates the tokens of the lead-in the way
	// chatloop estimates a summary.
	leadInBytesPerToken = 3
)

// The requests are model-only user rows, which users cannot author, so the
// headers identify the requests already in the window and cannot be forged
// from chat input.
const (
	tokenBudgetHandoffHeader = `<context_budget request="handoff">`
	tokenBudgetFinalHeader   = `<context_budget request="final">`
	tokenBudgetFooter        = `</context_budget>`
)

const newContextToolDescription = "Start a new context window for this chat. Call it when a " +
	"context_budget request asks you to, or when you finished a part of the work and the " +
	"conversation so far is no longer needed. First save a checkpoint in " + chatCheckpointNotePath +
	" with the notes tool: the goal, the decisions, the progress, what you learned and the next " +
	"steps. The tool refuses until you wrote or appended a note in this window. After the other " +
	"calls of this step finish, the conversation leaves your context and the next window starts " +
	"with a list of your notes. chat_history still finds the messages of earlier windows."

// tokenBudget is the ladder of one context window, in prompt tokens. The
// handoff request goes out at handoff, the final request at final, and the
// window rolls over at usable whether or not the model asked for it.
type tokenBudget struct {
	limit   int64
	usable  int64
	final   int64
	handoff int64
}

// newTokenBudget derives the ladder from the model's context limit and the
// chat's compression threshold, which is the per-model threshold or the
// user's override of it. A threshold of 100 or more turns the requests and
// the forced rollover off, as it turns automatic compaction off.
func newTokenBudget(limit int64, thresholdPercent int32) (tokenBudget, bool) {
	if limit <= 0 || thresholdPercent < 0 || thresholdPercent >= chatloop.CompactionDisabledThresholdPercent {
		return tokenBudget{}, false
	}
	threshold := max(thresholdPercent, tokenBudgetMinThresholdPercent)
	usable := limit - min(tokenBudgetMaxReserve, limit/4)
	final := max(usable-tokenBudgetFinalGap, usable/2)
	handoff := min(limit*int64(threshold)/100, final)
	return tokenBudget{limit: limit, usable: usable, final: final, handoff: handoff}, true
}

// usablePercent is the usable limit as a whole percent of the context limit,
// rounded down, for the compaction trigger the decision checks.
func (b tokenBudget) usablePercent() int32 {
	//nolint:gosec // The quotient is at most 100.
	return int32(b.usable * 100 / b.limit)
}

// tokenBudgetEligible reports whether a turn with tools works in token-budget
// windows: the experiment is on and the turn kept the notes, chat_history
// and new_context tools. The lead-in of a window points the model at
// chat_history, so a turn without it keeps summary compaction. Plan mode,
// Explore children and readonly or allowlisted custom agent types drop those
// tools, so they keep summary compaction too.
func (p *Server) tokenBudgetEligible(tools []fantasy.AgentTool) bool {
	_, ok := p.turnTokenBudgetFallback(tools)
	return ok
}

// turnTokenBudgetFallback returns the fallback of an eligible turn, which
// its new_context tool took from the organization setting when the turn's
// tools were built. Only the server's own new_context counts, so a dynamic
// or MCP tool of that name does not make a turn eligible.
func (p *Server) turnTokenBudgetFallback(tools []fantasy.AgentTool) (codersdk.ForkTokenBudgetFallback, bool) {
	if !p.experiments.Enabled(codersdk.ExperimentForkTokenBudget) {
		return "", false
	}
	has := func(name string) bool {
		return slices.ContainsFunc(tools, func(tool fantasy.AgentTool) bool {
			return tool.Info().Name == name
		})
	}
	if !has(chatNotesToolName) || !has(chatHistoryToolName) {
		return "", false
	}
	for _, tool := range tools {
		if tool.Info().Name != newContextToolName {
			continue
		}
		if snapshot, ok := tool.(tokenBudgetFallbackTool); ok {
			if fallback, ok := snapshot.tokenBudgetFallback(); ok {
				return fallback, true
			}
		}
	}
	return "", false
}

// tokenBudgetFallbackTool is the server's new_context tool, or a wrapper that
// forwards to it. ok is false for a wrapper of any other tool.
type tokenBudgetFallbackTool interface {
	tokenBudgetFallback() (fallback codersdk.ForkTokenBudgetFallback, ok bool)
}

// tokenBudgetTriggers replaces the automatic compaction trigger of an
// eligible turn. The window rolls over when the last step's prompt reached
// the usable limit, or when new_context succeeded in this window. With the
// new_window fallback the other triggers, including the compaction model
// override's, do not apply, since a rollover makes no summary. With the
// summary fallback the override writes the summary, so its trigger still
// applies and binds when it comes before the usable limit, as upstream's
// does before the threshold. The trigger the decision checks after a rollover
// becomes the usable limit of the chat model, so a window that starts at that
// limit fails as compaction that stayed over its limit does. Other turns get
// their triggers back unchanged.
func (p *Server) tokenBudgetTriggers(
	tools []fantasy.AgentTool,
	promptRows []database.ChatMessage,
	chatTrigger compactionTrigger,
	binding compactionTrigger,
	source compactionTriggerSource,
	other compactionTrigger,
	needed bool,
) (compactionTrigger, compactionTriggerSource, compactionTrigger, bool) {
	fallback, ok := p.turnTokenBudgetFallback(tools)
	if !ok {
		return binding, source, other, needed
	}
	override := other
	if source == compactionTriggerSourceOrganization {
		override = binding
	}
	usable := chatTrigger
	usage := latestPromptUsage(promptRows)
	needed = newContextRequested(promptRows)
	if budget, ok := newTokenBudget(chatTrigger.contextLimit, chatTrigger.thresholdPercent); ok {
		usable.thresholdPercent = budget.usablePercent()
		needed = needed || contextTokensFromUsage(usage) >= budget.usable
	}
	if fallback != codersdk.ForkTokenBudgetFallbackSummary || !override.enabled() {
		return usable, compactionTriggerSourceChat, compactionTrigger{}, needed
	}
	if bindingCompactionTriggerSource(usable, override) == compactionTriggerSourceOrganization {
		needed = needed || shouldCompactPromptUsage(usage, override.contextLimit, override.thresholdPercent)
		return override, compactionTriggerSourceOrganization, usable, needed
	}
	return usable, compactionTriggerSourceChat, override, needed
}

// newContextRequested reports whether a new_context call succeeded in the
// window promptRows hold. The prompt holds only the current window, so a call
// of an earlier window does not count.
func newContextRequested(promptRows []database.ChatMessage) bool {
	for _, row := range promptRows {
		if row.Deleted || row.Compressed || row.Role != database.ChatMessageRoleTool {
			continue
		}
		parts, err := chatprompt.ParseContent(row)
		if err != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == codersdk.ChatMessagePartTypeToolResult && part.ToolName == newContextToolName && !part.IsError {
				return true
			}
		}
	}
	return false
}

// forkGenerateCompaction runs one compaction. An automatic compaction of an
// eligible turn is a rollover: it starts the next window with a lead-in and
// makes no model call. When the turn's fallback is summary, a rollover that
// the model did not ask for with new_context summarizes as upstream does. A
// manual compaction and every other turn summarize as upstream does. The
// fallback comes from the turn's tools, so the compaction follows the setting
// the turn was built with even if an admin changed it since.
func (p *Server) forkGenerateCompaction(ctx context.Context, prepared turnEnvironment, opts chatloop.GenerateCompactionOptions) (chatloop.CompactionResult, error) {
	if opts.Source != chatloop.CompactionSourceAutomatic {
		return chatloop.GenerateCompaction(ctx, opts)
	}
	fallback, ok := p.turnTokenBudgetFallback(prepared.Tools)
	if !ok {
		return chatloop.GenerateCompaction(ctx, opts)
	}
	if fallback == codersdk.ForkTokenBudgetFallbackSummary {
		promptRows := prepared.PromptRows
		if promptRows == nil {
			var err error
			promptRows, err = p.db.GetChatMessagesForPromptByChatID(ctx, prepared.Chat.ID)
			if err != nil {
				return chatloop.CompactionResult{}, xerrors.Errorf("get chat messages for prompt: %w", err)
			}
		}
		if !newContextRequested(promptRows) {
			return chatloop.GenerateCompaction(ctx, opts)
		}
	}
	return p.contextWindowRollover(ctx, prepared.Chat, opts)
}

// organizationTokenBudget returns the compaction setting of an organization,
// or the default when it never saved one. buildTurnEnvironment reads it once
// per step, through appendForkTools, to decide whether the turn gets
// new_context, which keeps the fallback for the rest of the step. A turn
// without new_context is not eligible, so it takes upstream's path: summary
// compaction and no requests. tokenBudgetMessage reads it before the step
// only when a request is due.
func (p *Server) organizationTokenBudget(ctx context.Context, organizationID uuid.UUID) (codersdk.ForkOrganizationTokenBudget, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return codersdk.ForkOrganizationTokenBudget{}, xerrors.Errorf("fork queries: %w", err)
	}
	//nolint:gocritic // Chat owners cannot read organization config, so chatd reads it.
	row, err := fdb.GetChatOrganizationTokenBudget(dbauthz.AsChatd(ctx), organizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return codersdk.DefaultForkOrganizationTokenBudget(), nil
	}
	if err != nil {
		return codersdk.ForkOrganizationTokenBudget{}, xerrors.Errorf("get token budget setting of organization %s: %w", organizationID, err)
	}
	return codersdk.ForkOrganizationTokenBudget{
		Strategy: codersdk.ForkTokenBudgetStrategy(row.Strategy),
		Fallback: codersdk.ForkTokenBudgetFallback(row.Fallback),
	}, nil
}

// tokenBudgetTools returns the fork-token-budget tools of a turn of chat.
// notes and chat_history stay with the summary strategy, since they help
// without windows, and new_context comes only with the token_budget
// strategy.
func (p *Server) tokenBudgetTools(ctx context.Context, chat database.Chat) ([]fantasy.AgentTool, error) {
	tools := []fantasy.AgentTool{p.chatNotesTool(chat), p.chatHistoryTool(chat)}
	setting, err := p.organizationTokenBudget(ctx, chat.OrganizationID)
	if err != nil {
		return nil, err
	}
	if setting.Strategy == codersdk.ForkTokenBudgetStrategyTokenBudget {
		tools = append(tools, p.newContextTool(chat, setting.Fallback))
	}
	return tools, nil
}

// contextWindowRollover returns the compaction result that starts the next
// window of chat. Its system summary is the lead-in the model reads, and its
// report is the line the user sees. The model context of a pre_compact hook,
// which a summary would carry, goes into the lead-in. The result names the
// window and its trigger, so the UI shows a window divider.
func (p *Server) contextWindowRollover(ctx context.Context, chat database.Chat, opts chatloop.GenerateCompactionOptions) (chatloop.CompactionResult, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return chatloop.CompactionResult{}, xerrors.Errorf("fork queries: %w", err)
	}
	rows, err := fdb.ListChatHistoryRows(ctx, chat.ID)
	if err != nil {
		return chatloop.CompactionResult{}, xerrors.Errorf("read chat history: %w", err)
	}
	notes, err := fdb.ListChatNotes(ctx, chat.ID)
	if err != nil {
		return chatloop.CompactionResult{}, xerrors.Errorf("list notes: %w", err)
	}
	promptRows, err := p.db.GetChatMessagesForPromptByChatID(ctx, chat.ID)
	if err != nil {
		return chatloop.CompactionResult{}, xerrors.Errorf("get chat messages for prompt: %w", err)
	}
	trigger := chatloop.ContextWindowTriggerContextLimit
	if newContextRequested(promptRows) {
		trigger = chatloop.ContextWindowTriggerNewContext
	}
	windowID := "w:" + strconv.Itoa(len(chatHistoryWindows(chat, rows))+1)
	leadIn := formatContextWindowLeadIn(windowID, notes, opts.SummaryHint)
	report := "Context window " + windowID + " started from the notes of this chat. No summary was made."

	contextTokens := contextTokensFromUsage(opts.StepUsage)
	var usagePercent float64
	if opts.ContextLimit > 0 {
		usagePercent = float64(contextTokens) / float64(opts.ContextLimit) * 100
	}
	result := chatloop.CompactionResult{
		SystemSummary:          leadIn,
		SummaryReport:          report,
		Source:                 chatloop.CompactionSourceAutomatic,
		ThresholdPercent:       opts.ThresholdPercent,
		UsagePercent:           usagePercent,
		ContextTokens:          contextTokens,
		ChatContextLimit:       opts.ChatContextLimit,
		EstimatedContextTokens: int64((len(leadIn) + leadInBytesPerToken - 1) / leadInBytesPerToken),
		TriggerContextLimit:    opts.ContextLimit,
		ContextWindow:          &chatloop.ContextWindowBoundary{ID: windowID, Trigger: trigger},
	}
	if opts.PublishMessagePart != nil && opts.ToolCallID != "" {
		opts.PublishMessagePart(codersdk.ChatMessageRoleAssistant, codersdk.ChatMessageToolCall(opts.ToolCallID, opts.ToolName, nil))
		resultJSON, err := json.Marshal(chatloop.CompactionToolResult{
			Summary:                   report,
			Source:                    result.Source,
			ThresholdPercent:          result.ThresholdPercent,
			UsagePercent:              result.UsagePercent,
			ContextTokens:             result.ContextTokens,
			ContextLimitTokens:        result.ChatContextLimit,
			EstimatedContextTokens:    result.EstimatedContextTokens,
			TriggerContextLimitTokens: result.TriggerContextLimit,
		})
		if err != nil {
			return chatloop.CompactionResult{}, xerrors.Errorf("marshal rollover result: %w", err)
		}
		opts.PublishMessagePart(codersdk.ChatMessageRoleTool, codersdk.ChatMessageToolResult(opts.ToolCallID, opts.ToolName, withContextWindowResult(resultJSON, result.ContextWindow), false, false))
	}
	return result, nil
}

// withContextWindowResult adds window to a chat_summarized tool result as
// context_window. buildCompactionMessages calls it for the persisted result.
// A result it cannot decode stays as it is, which the UI shows as a summary.
func withContextWindowResult(result json.RawMessage, window *chatloop.ContextWindowBoundary) json.RawMessage {
	if window == nil {
		return result
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil || fields == nil {
		return result
	}
	encoded, err := json.Marshal(window)
	if err != nil {
		return result
	}
	fields["context_window"] = encoded
	merged, err := json.Marshal(fields)
	if err != nil {
		return result
	}
	return merged
}

// formatContextWindowLeadIn renders the lead-in of window windowID. It lists
// the notes and points the model to its checkpoint, and holds nothing of the
// earlier conversation except hookContext, the model context of a pre_compact
// hook.
func formatContextWindowLeadIn(windowID string, notes []forkdb.ListChatNotesRow, hookContext string) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "<context_window id=%q>\n", windowID)
	_, _ = sb.WriteString("A new context window started. The earlier conversation of this chat is no longer in your context; your notes are.\n")
	if len(notes) == 0 {
		_, _ = sb.WriteString("This chat has no notes.\n")
	} else {
		_, _ = sb.WriteString("Notes:\n")
		for _, note := range notes {
			_, _ = fmt.Fprintf(&sb, "- %s (%d bytes)\n", note.Path, note.Bytes)
		}
	}
	hasCheckpoint := slices.ContainsFunc(notes, func(note forkdb.ListChatNotesRow) bool {
		return note.Path == chatCheckpointNotePath
	})
	if hasCheckpoint {
		_, _ = sb.WriteString("Read " + chatCheckpointNotePath + " with the notes tool first, then continue from its next steps.\n")
	} else {
		_, _ = sb.WriteString("There is no " + chatCheckpointNotePath + ". Read the notes you have, and use chat_history to recover the task.\n")
	}
	_, _ = sb.WriteString("To find a message of an earlier window, use chat_history.\n")
	if hookContext = strings.TrimSpace(hookContext); hookContext != "" {
		_, _ = sb.WriteString("Context from a pre_compact hook:\n")
		_, _ = sb.WriteString(hookContext + "\n")
	}
	_, _ = sb.WriteString("</context_window>")
	return sb.String()
}

// newContextTool returns the new_context tool of chat. It runs after the
// other calls of its step, in call order, so a notes write in the same step
// counts.
func (p *Server) newContextTool(chat database.Chat, fallback codersdk.ForkTokenBudgetFallback) fantasy.AgentTool {
	return serialNewContextTool{fallback: fallback, AgentTool: fantasy.NewAgentTool(
		newContextToolName,
		newContextToolDescription,
		func(ctx context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			changed, err := p.notesChangedInWindow(ctx, chat)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			if !changed {
				return fantasy.NewTextErrorResponse("No note changed in this context window. Save a checkpoint in " +
					chatCheckpointNotePath + " with the notes tool first: the goal, the decisions, the progress, " +
					"what you learned and the next steps. Then call new_context again."), nil
			}
			return toolJSONResponse(map[string]string{
				"status":  "accepted",
				"message": "The next context window starts after the other calls of this step finish. It begins with the list of your notes.",
			}), nil
		},
	)}
}

// serialNewContextTool runs new_context after the concurrent calls of its
// step and in call order with the notes calls. It holds the fallback of the
// organization setting its turn was built with.
type serialNewContextTool struct {
	fantasy.AgentTool
	fallback codersdk.ForkTokenBudgetFallback
}

func (serialNewContextTool) SerialToolCalls() bool { return true }

func (t serialNewContextTool) tokenBudgetFallback() (codersdk.ForkTokenBudgetFallback, bool) {
	return t.fallback, true
}

// notesChangedInWindow reports whether a note of chat was written or appended
// in the current window. In the first window every note counts, and in a
// later one a note counts when it changed after the window began. Both times
// come from the database clock.
func (p *Server) notesChangedInWindow(ctx context.Context, chat database.Chat) (bool, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return false, xerrors.Errorf("fork queries: %w", err)
	}
	notes, err := fdb.ListChatNotes(ctx, chat.ID)
	if err != nil {
		return false, xerrors.Errorf("list notes: %w", err)
	}
	if len(notes) == 0 {
		return false, nil
	}
	rows, err := fdb.ListChatHistoryRows(ctx, chat.ID)
	if err != nil {
		return false, xerrors.Errorf("read chat history: %w", err)
	}
	windows := chatHistoryWindows(chat, rows)
	current := windows[len(windows)-1]
	if current.after == 0 {
		return true, nil
	}
	return slices.ContainsFunc(notes, func(note forkdb.ListChatNotesRow) bool {
		return note.UpdatedAt.After(current.StartedAt)
	}), nil
}

// tokenBudgetMessage returns the handoff or final request that is due for
// chat, or ok=false when none is. It mirrors tokenBudgetEligible before the
// turn's tools exist: plan mode, Explore children and custom agent types
// whose policy drops the notes, chat_history or new_context tool get no
// requests.
//
// promptRows are the rows the prompt is built from, loaded by the caller; nil
// loads them here, so a caller outside syncForkContext still works.
func (p *Server) tokenBudgetMessage(ctx context.Context, chat database.Chat, promptRows []database.ChatMessage) (chatstate.Message, bool, error) {
	if !p.experiments.Enabled(codersdk.ExperimentForkTokenBudget) || chat.LastModelConfigID == uuid.Nil {
		return chatstate.Message{}, false, nil
	}
	eligible, err := p.tokenBudgetChatEligible(ctx, chat)
	if err != nil || !eligible {
		return chatstate.Message{}, false, err
	}
	config, err := p.db.GetChatModelConfigByID(ctx, chat.LastModelConfigID)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("load model config: %w", err)
	}
	threshold := config.CompressionThreshold
	override, ok, err := p.resolveUserCompactionThreshold(ctx, chat.OwnerID, config.ID)
	if err != nil {
		return chatstate.Message{}, false, err
	}
	if ok {
		threshold = override
	}
	budget, ok := newTokenBudget(config.ContextLimit, threshold)
	if !ok {
		return chatstate.Message{}, false, nil
	}
	if promptRows == nil {
		promptRows, err = p.db.GetChatMessagesForPromptByChatID(ctx, chat.ID)
		if err != nil {
			return chatstate.Message{}, false, xerrors.Errorf("load prompt messages: %w", err)
		}
	}
	if deferred, err := forkContextDeferred(chat, promptRows); err != nil || deferred {
		return chatstate.Message{}, false, err
	}
	header, tokens, ok := tokenBudgetRequestDue(budget, promptRows)
	if !ok {
		return chatstate.Message{}, false, nil
	}
	// Read the setting only when a request is due, so a step that sends
	// none does not read it here.
	setting, err := p.organizationTokenBudget(ctx, chat.OrganizationID)
	if err != nil || setting.Strategy != codersdk.ForkTokenBudgetStrategyTokenBudget {
		return chatstate.Message{}, false, err
	}
	message, err := forkContextTextMessage(chat, formatTokenBudgetRequest(header, budget, tokens))
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("marshal context budget request: %w", err)
	}
	return message, true, nil
}

// tokenBudgetChatEligible reports whether the turns of chat keep the notes,
// chat_history and new_context tools, by running them through the filters
// buildTurnEnvironment applies.
func (p *Server) tokenBudgetChatEligible(ctx context.Context, chat database.Chat) (bool, error) {
	if isExploreSubagentMode(chat.Mode) {
		return false, nil
	}
	tools := filterToolsForTurn([]fantasy.AgentTool{p.chatNotesTool(chat), p.chatHistoryTool(chat), p.newContextTool(chat, codersdk.ForkTokenBudgetFallbackNewWindow)}, chat.PlanMode, chat.ParentChatID, nil)
	policy, err := p.customAgentToolPolicyOfChat(ctx, chat)
	if err != nil {
		return false, err
	}
	if policy != nil {
		tools, _, _ = policy.filter(tools, nil, nil, nil)
	}
	return len(tools) == 3, nil
}

// tokenBudgetRequestDue returns the header of the request that is due in the
// window promptRows hold, and the prompt tokens of its last step. Each
// request goes out once per window, and the final request replaces a handoff
// request that was not sent yet. A window whose first step already reached
// the handoff point gets no requests, because a new window would start there
// too, and a window at the usable limit gets none because it rolls over. A
// window whose new_context call succeeded gets none either, since it rolls
// over before the next step.
func tokenBudgetRequestDue(budget tokenBudget, promptRows []database.ChatMessage) (string, int64, bool) {
	var (
		first, last            int64
		handoffSent, finalSent bool
	)
	for _, row := range promptRows {
		if row.Deleted || row.Compressed {
			continue
		}
		if row.Role == database.ChatMessageRoleAssistant {
			if tokens := contextTokensFromUsage(usageFromMessage(row)); tokens > 0 {
				if first == 0 {
					first = tokens
				}
				last = tokens
			}
			continue
		}
		if row.Role != database.ChatMessageRoleUser || row.Visibility != database.ChatMessageVisibilityModel {
			continue
		}
		parts, err := chatprompt.ParseContent(row)
		if err != nil {
			continue
		}
		for _, part := range parts {
			if part.Type != codersdk.ChatMessagePartTypeText {
				continue
			}
			handoffSent = handoffSent || strings.HasPrefix(part.Text, tokenBudgetHandoffHeader)
			finalSent = finalSent || strings.HasPrefix(part.Text, tokenBudgetFinalHeader)
		}
	}
	switch {
	case first == 0 || first >= budget.handoff || last >= budget.usable || finalSent || newContextRequested(promptRows):
		return "", 0, false
	case last >= budget.final:
		return tokenBudgetFinalHeader, last, true
	case last >= budget.handoff && !handoffSent:
		return tokenBudgetHandoffHeader, last, true
	default:
		return "", 0, false
	}
}

// formatTokenBudgetRequest renders the request that header names.
func formatTokenBudgetRequest(header string, budget tokenBudget, tokens int64) string {
	var body string
	if header == tokenBudgetFinalHeader {
		body = fmt.Sprintf("This context window is almost full: its last prompt took %d of %d tokens. "+
			"Update %s with the notes tool now and call new_context. At %d tokens the window rolls over "+
			"without waiting for you.", tokens, budget.limit, chatCheckpointNotePath, budget.usable)
	} else {
		body = fmt.Sprintf("This context window is filling up: its last prompt took %d of %d tokens. "+
			"Save a checkpoint in %s with the notes tool: the goal, the decisions, the progress, what you "+
			"learned and the next steps. Then finish the step you are on and call new_context to continue "+
			"in a fresh window, which starts from your notes and not from this conversation. At %d tokens "+
			"the window rolls over without waiting for you.", tokens, budget.limit, chatCheckpointNotePath, budget.usable)
	}
	return header + "\n" + body + "\n" + tokenBudgetFooter
}
