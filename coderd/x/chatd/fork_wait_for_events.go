package chatd

// This file is fork code: it holds the wait_for_events chat tool of the
// fork-chat-wakeups experiment and the worker delivery of its events
// wake-ups. appendForkTools adds the tool to a turn.

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

const (
	waitForEventsToolName      = "wait_for_events"
	defaultWaitForEventsPrompt = "A child agent you were waiting for is done. Read its result with wait_agent, then continue."
	defaultWaitForEventsExpiry = 24 * time.Hour
	maxWaitForEventsChildren   = 50

	wakeupEventChildDone = "child_done"
)

type waitForEventsArgs struct {
	ChildChatIDs     []string `json:"child_chat_ids,omitempty" description:"IDs of the direct child agents of this chat to wait for. Defaults to every child agent that is still working."`
	Prompt           string   `json:"prompt,omitempty" description:"Text that starts every wake-up message. Defaults to a request to read the child's result with wait_agent."`
	ExpiresInSeconds *int     `json:"expires_in_seconds,omitempty" description:"Stop waiting this many seconds from now. Defaults to 86400 (24 hours)."`
}

type waitForEventsChild struct {
	ChatID uuid.UUID           `json:"chat_id"`
	Title  string              `json:"title"`
	Status database.ChatStatus `json:"status"`
}

type waitForEventsResult struct {
	ID        uuid.UUID            `json:"id"`
	ExpiresAt time.Time            `json:"expires_at"`
	Children  []waitForEventsChild `json:"children"`
}

// wakeupEvent is one entry of the events of an events wake-up. Type names
// what the worker waits for; entries of other types are ignored.
type wakeupEvent struct {
	Type   string    `json:"type"`
	ChatID uuid.UUID `json:"chat_id"`
}

// waitForEventsTool returns the wait_for_events tool, which creates an events
// wake-up of chat for its child agents.
func (p *Server) waitForEventsTool(chat database.Chat) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		waitForEventsToolName,
		"Wait for child agents without blocking: call this, then end your "+
			"turn. This chat then receives one user message per watched child "+
			"agent when that child is done, so you can read its result with "+
			"wait_agent and continue. Watches child_chat_ids, or by default "+
			"every child agent that is still working. A message never "+
			"interrupts a running turn. Stops after expires_in_seconds "+
			"(default 24 hours). Returns the wake-up ID and the watched children.",
		func(ctx context.Context, args waitForEventsArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			children, err := p.waitForEventsChildren(ctx, chat, args.ChildChatIDs)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			spec, err := waitForEventsSpec(args, children)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			wakeup, err := p.createChatWakeup(ctx, p.db, chat, spec, p.clock.Now())
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			result := waitForEventsResult{
				ID:        wakeup.ID,
				ExpiresAt: wakeup.ExpiresAt.Time,
				Children:  make([]waitForEventsChild, 0, len(children)),
			}
			for _, child := range children {
				result.Children = append(result.Children, waitForEventsChild{ChatID: child.ID, Title: child.Title, Status: child.Status})
			}
			return toolJSONResponse(result), nil
		},
	)
}

// waitForEventsChildren returns the children of chat to watch: the direct
// children named by ids, or every unarchived child that is not done.
func (p *Server) waitForEventsChildren(ctx context.Context, chat database.Chat, ids []string) ([]database.Chat, error) {
	rows, err := p.db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
		ParentIds: []uuid.UUID{chat.ID},
	})
	if err != nil {
		return nil, xerrors.Errorf("list child agents: %w", err)
	}
	children := make([]database.Chat, 0, len(rows))
	for _, row := range rows {
		children = append(children, row.Chat)
	}
	slices.SortStableFunc(children, func(a, b database.Chat) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID.String(), b.ID.String()))
	})

	var watched []database.Chat
	if len(ids) > 0 {
		for _, raw := range ids {
			id, err := uuid.Parse(strings.TrimSpace(raw))
			if err != nil {
				return nil, xerrors.Errorf("invalid child chat ID %q", raw)
			}
			index := slices.IndexFunc(children, func(child database.Chat) bool { return child.ID == id })
			if index < 0 {
				return nil, xerrors.Errorf("chat %s is not a child of this chat", id)
			}
			if !slices.ContainsFunc(watched, func(child database.Chat) bool { return child.ID == id }) {
				watched = append(watched, children[index])
			}
		}
	} else {
		for _, child := range children {
			if child.Archived {
				continue
			}
			done, err := p.childDone(ctx, child)
			if err != nil {
				return nil, err
			}
			if !done {
				watched = append(watched, child)
			}
		}
	}
	if len(watched) == 0 {
		return nil, xerrors.New("no running child agents to wait for")
	}
	if len(watched) > maxWaitForEventsChildren {
		watched = watched[:maxWaitForEventsChildren]
	}
	return watched, nil
}

// waitForEventsSpec maps wait_for_events arguments to the spec of an events
// wake-up with one child_done event per child.
func waitForEventsSpec(args waitForEventsArgs, children []database.Chat) (wakeupSpec, error) {
	spec := wakeupSpec{
		source:           wakeupSourceEvents,
		kind:             forkdb.ChatWakeupKindEvents,
		prompt:           args.Prompt,
		expiresInSeconds: int(defaultWaitForEventsExpiry / time.Second),
	}
	if strings.TrimSpace(spec.prompt) == "" {
		spec.prompt = defaultWaitForEventsPrompt
	}
	if args.ExpiresInSeconds != nil {
		maxSeconds := int(maxWakeupPeriod / time.Second)
		if *args.ExpiresInSeconds <= 0 || *args.ExpiresInSeconds > maxSeconds {
			return wakeupSpec{}, xerrors.Errorf("expires_in_seconds must be between 1 and %d, got %d", maxSeconds, *args.ExpiresInSeconds)
		}
		spec.expiresInSeconds = *args.ExpiresInSeconds
	}
	events := make([]wakeupEvent, 0, len(children))
	for _, child := range children {
		events = append(events, wakeupEvent{Type: wakeupEventChildDone, ChatID: child.ID})
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return wakeupSpec{}, xerrors.Errorf("encode events: %w", err)
	}
	spec.events = raw
	return spec, nil
}

// deliverEventsWakeup sends one message for the first watched child that is
// done and has no run yet, and completes the wake-up once every watched child
// has a run or is gone. deliverWakeup calls it only for an idle chat, so the
// next child is delivered on a later tick, after the turn this one starts.
func (p *Server) deliverEventsWakeup(ctx context.Context, fdb forkdb.Store, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time) error {
	var events []wakeupEvent
	if err := json.Unmarshal(wakeup.Events, &events); err != nil {
		p.logger.Warn(ctx, "chat wake-up has invalid events",
			slog.F("wakeup_id", wakeup.ID),
			slog.Error(err),
		)
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: "invalid_events"})
	}
	rows, err := p.db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
		ParentIds: []uuid.UUID{chat.ID},
	})
	if err != nil {
		return xerrors.Errorf("list child agents: %w", err)
	}
	keys, err := fdb.ListChatWakeupRunKeys(ctx, wakeup.ID)
	if err != nil {
		return xerrors.Errorf("list wake-up runs: %w", err)
	}

	var next *database.Chat
	// Watched children, other than next, that still need a run.
	pending := 0
	for _, event := range events {
		if event.Type != wakeupEventChildDone || slices.Contains(keys, childDoneRunKey(event.ChatID)) {
			continue
		}
		index := slices.IndexFunc(rows, func(row database.GetChildChatsByParentIDsRow) bool { return row.Chat.ID == event.ChatID })
		if index < 0 {
			// A child that is gone has nothing to report.
			continue
		}
		child := rows[index].Chat
		if next == nil {
			done, err := p.childDone(ctx, child)
			if err != nil {
				return err
			}
			if done {
				next = &child
				continue
			}
		}
		pending++
	}
	if next == nil {
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, eventsWakeupFinish(wakeup, now, 0, pending))
	}
	delivery := wakeupDelivery{
		text:   fmt.Sprintf("%s\n\nChild agent %q (%s) is done with status %s.", wakeup.Prompt, next.Title, next.ID, next.Status),
		runKey: childDoneRunKey(next.ID),
		finish: eventsWakeupFinish(wakeup, now, 1, pending),
	}
	err = p.sendWakeupPrompt(ctx, chat, wakeup, now, delivery)
	return p.finishWakeupSend(ctx, fdb, wakeup, now, delivery.runKey, err)
}

// eventsWakeupFinish completes an events wake-up when no watched child still
// needs a run, and otherwise polls again.
func eventsWakeupFinish(wakeup forkdb.ChatWakeup, now time.Time, runDelta int32, pending int) wakeupFinish {
	if pending == 0 {
		return wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "done", runDelta: runDelta}
	}
	return nextWakeupRun(wakeup, now, runDelta)
}

// childDoneRunKey names the run of a child_done event, so each child wakes
// the chat once.
func childDoneRunKey(childID uuid.UUID) string {
	return wakeupEventChildDone + ":" + childID.String()
}

// childDone reports whether a child agent finished its work: it is archived,
// or it settled in a status other than running, interrupting or paused (the
// rule of checkSubagentCompletion) and does not wait for an answer.
func (p *Server) childDone(ctx context.Context, child database.Chat) (bool, error) {
	if child.Archived {
		return true, nil
	}
	switch child.Status {
	case database.ChatStatusRunning, database.ChatStatusInterrupting, database.ChatStatusPaused:
		return false, nil
	}
	awaits, err := p.childAwaitsAnswer(ctx, child.ID)
	if err != nil {
		return false, xerrors.Errorf("check questions of child %s: %w", child.ID, err)
	}
	return !awaits, nil
}

// childAwaitsAnswer reports whether a child is waiting for an answer to a
// descendant question, which does not count as done.
// TODO(ibetitsmike): call pendingDescendantQuestion once #86 is on main.
func (*Server) childAwaitsAnswer(context.Context, uuid.UUID) (bool, error) { return false, nil }
