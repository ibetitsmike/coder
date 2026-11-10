package chatd

// This file is fork code: it holds the wait_for_events chat tool of the
// fork-chat-wakeups experiment and the worker delivery of its events
// wake-ups. appendForkTools adds the tool to a turn.

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	waitForEventsToolName      = "wait_for_events"
	defaultWaitForEventsPrompt = "A child agent you were waiting for is done. Read its result with wait_agent, then continue."
	defaultWaitForEventsExpiry = 24 * time.Hour
	maxWaitForEventsChildren   = 50

	wakeupEventChildDone = "child_done"
)

type waitForEventsArgs struct {
	ChildChatIDs      []string `json:"child_chat_ids,omitempty" description:"IDs of the direct child agents of this chat to wait for. Defaults to every child agent that is still working. At most 50."`
	Prompt            string   `json:"prompt,omitempty" description:"Text that starts every wake-up message. Defaults to a request to read the child's result with wait_agent."`
	ExpiresInSeconds  *int     `json:"expires_in_seconds,omitempty" description:"Stop waiting this many seconds from now, at least 60. Defaults to 86400 (24 hours)."`
	PullRequestURL    string   `json:"pull_request_url,omitempty" description:"A GitHub pull request URL, e.g. https://github.com/org/repo/pull/7, to watch. The chat gets one message when it has new activity by someone other than the chat owner. Without child_chat_ids, only the pull request is watched."`
	PullRequestEvents []string `json:"pull_request_events,omitempty" description:"Which pull request activity wakes the chat: any of review, comment, checks (CI settled on a new result) and closed (closed or merged). Defaults to all four. Requires pull_request_url."`
}

type waitForEventsChild struct {
	ChatID uuid.UUID           `json:"chat_id"`
	Title  string              `json:"title"`
	Status database.ChatStatus `json:"status"`
}

type waitForEventsResult struct {
	ID          uuid.UUID                 `json:"id"`
	ExpiresAt   time.Time                 `json:"expires_at"`
	Children    []waitForEventsChild      `json:"children"`
	PullRequest *waitForEventsPullRequest `json:"pull_request,omitempty"`
}

// wakeupEvent is one entry of the events of an events wake-up. Type names
// what the worker waits for; entries of other types are ignored. A child_done
// entry sets ChatID, a pull_request entry the other fields: Since is when the
// watch started, and HeadSHA and ChecksState are the baseline at that time.
type wakeupEvent struct {
	Type        string    `json:"type"`
	ChatID      uuid.UUID `json:"chat_id,omitzero"`
	URL         string    `json:"url,omitempty"`
	On          []string  `json:"on,omitempty"`
	Since       time.Time `json:"since,omitzero"`
	HeadSHA     string    `json:"head_sha,omitempty"`
	ChecksState string    `json:"checks_state,omitempty"`
	// Seen lists the URLs of reviews and comments the baseline read already
	// listed.
	Seen []string `json:"seen,omitempty"`
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
			"(default 24 hours). Returns the wake-up ID and the watched children. "+
			"With pull_request_url, it also watches that GitHub pull request "+
			"and sends one message when someone else reviews or comments, CI "+
			"settles, or it closes.",
		func(ctx context.Context, args waitForEventsArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			id, ok := workspacesdk.ToolCallIDFromContext(ctx)
			if !ok {
				return fantasy.NewTextErrorResponse("wait_for_events requires a deterministic tool-call ID"), nil
			}
			// A replayed call returns the wake-up it created, even when the
			// children changed since and the watch set would differ now.
			wakeup, err := p.chatWakeupByID(ctx, chat, id)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			if wakeup == nil {
				spec, err := p.newWaitForEventsSpec(ctx, chat, args)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				created, err := p.createChatWakeup(ctx, p.db, chat, id, chat.OwnerID, spec, p.clock.Now())
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				wakeup = &created
			}
			result, err := p.waitForEventsResponse(ctx, chat, *wakeup)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return toolJSONResponse(result), nil
		},
	)
}

// chatWakeupByID returns the wake-up of chat with id, or nil when chat has
// none.
func (p *Server) chatWakeupByID(ctx context.Context, chat database.Chat, id uuid.UUID) (*forkdb.ChatWakeup, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, xerrors.Errorf("fork queries: %w", err)
	}
	wakeup, err := fdb.GetChatWakeupByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && wakeup.ChatID != chat.ID) {
		return nil, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("get chat wake-up: %w", err)
	}
	return &wakeup, nil
}

// newWaitForEventsSpec resolves what a new wait_for_events call watches: its
// pull request, its children, or both, and maps it to a wake-up spec.
func (p *Server) newWaitForEventsSpec(ctx context.Context, chat database.Chat, args waitForEventsArgs) (wakeupSpec, error) {
	var pullRequest *wakeupEvent
	switch {
	case strings.TrimSpace(args.PullRequestURL) != "":
		event, err := p.pullRequestWaitEvent(ctx, chat, args.PullRequestURL, args.PullRequestEvents)
		if err != nil {
			return wakeupSpec{}, err
		}
		pullRequest = &event
	case len(args.PullRequestEvents) > 0:
		return wakeupSpec{}, xerrors.New("pull_request_events requires pull_request_url")
	}
	var children []database.Chat
	// A call that names only a pull request waits for no children.
	if pullRequest == nil || len(args.ChildChatIDs) > 0 {
		var err error
		children, err = p.waitForEventsChildren(ctx, chat, args.ChildChatIDs)
		if err != nil {
			return wakeupSpec{}, err
		}
	}
	return waitForEventsSpec(args, children, pullRequest)
}

// waitForEventsResponse describes wakeup with the current state of the
// children its events watch and the baseline of its pull request.
func (p *Server) waitForEventsResponse(ctx context.Context, chat database.Chat, wakeup forkdb.ChatWakeup) (waitForEventsResult, error) {
	var events []wakeupEvent
	if err := json.Unmarshal(wakeup.Events, &events); err != nil {
		return waitForEventsResult{}, xerrors.Errorf("decode events: %w", err)
	}
	rows, err := p.db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
		ParentIds: []uuid.UUID{chat.ID},
	})
	if err != nil {
		return waitForEventsResult{}, xerrors.Errorf("list child agents: %w", err)
	}
	result := waitForEventsResult{
		ID:        wakeup.ID,
		ExpiresAt: wakeup.ExpiresAt.Time,
		Children:  make([]waitForEventsChild, 0, len(events)),
	}
	for _, event := range events {
		if event.Type == wakeupEventPullRequest {
			result.PullRequest = &waitForEventsPullRequest{URL: event.URL, On: event.On, HeadSHA: event.HeadSHA, ChecksState: event.ChecksState}
			continue
		}
		index := slices.IndexFunc(rows, func(row database.GetChildChatsByParentIDsRow) bool { return row.Chat.ID == event.ChatID })
		if event.Type != wakeupEventChildDone || index < 0 {
			continue
		}
		child := rows[index].Chat
		result.Children = append(result.Children, waitForEventsChild{ChatID: child.ID, Title: child.Title, Status: child.Status})
	}
	return result, nil
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
		return nil, xerrors.Errorf("cannot wait for %d child agents at once, the maximum is %d", len(watched), maxWaitForEventsChildren)
	}
	return watched, nil
}

// waitForEventsSpec maps wait_for_events arguments to the spec of an events
// wake-up with one child_done event per child, then pullRequest if set.
func waitForEventsSpec(args waitForEventsArgs, children []database.Chat, pullRequest *wakeupEvent) (wakeupSpec, error) {
	spec := wakeupSpec{
		source:           wakeupSourceEvents,
		kind:             forkdb.ChatWakeupKindEvents,
		prompt:           args.Prompt,
		expiresInSeconds: int(defaultWaitForEventsExpiry / time.Second),
	}
	if strings.TrimSpace(spec.prompt) == "" {
		spec.prompt = defaultWaitForEventsPrompt
		if pullRequest != nil {
			spec.prompt = defaultPullRequestWaitPrompt
		}
	}
	if args.ExpiresInSeconds != nil {
		maxSeconds := int(maxWakeupPeriod / time.Second)
		minSeconds := int(minWakeupPeriod / time.Second)
		if *args.ExpiresInSeconds < minSeconds || *args.ExpiresInSeconds > maxSeconds {
			return wakeupSpec{}, xerrors.Errorf("expires_in_seconds must be between %d and %d, got %d", minSeconds, maxSeconds, *args.ExpiresInSeconds)
		}
		spec.expiresInSeconds = *args.ExpiresInSeconds
	}
	events := make([]wakeupEvent, 0, len(children))
	for _, child := range children {
		events = append(events, wakeupEvent{Type: wakeupEventChildDone, ChatID: child.ID})
	}
	if pullRequest != nil {
		events = append(events, *pullRequest)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return wakeupSpec{}, xerrors.Errorf("encode events: %w", err)
	}
	spec.events = raw
	return spec, nil
}

// deliverEventsWakeup sends one message for the first watched child that is
// done, or pull request with new activity, that has no run yet, and completes
// the wake-up once every watched entry has a run or is gone. deliverWakeup
// calls it only for an idle chat, so the next entry is delivered on a later
// tick, after the turn this one starts.
func (p *Server) deliverEventsWakeup(ctx context.Context, fdb forkdb.Store, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time) error {
	var events []wakeupEvent
	if err := json.Unmarshal(wakeup.Events, &events); err != nil {
		p.logger.Warn(ctx, "chat wake-up has invalid events",
			slog.F("wakeup_id", wakeup.ID),
			slog.Error(err),
		)
		return p.finishWakeupClaim(ctx, fdb, chat, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: "invalid_events"})
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

	// The entry this tick delivers.
	var next *wakeupDelivery
	// Watched entries, other than next, that still need a run.
	pending := 0
	// Whether one of them is a pull request, which is polled less often.
	pullRequestPending := false
	for _, event := range events {
		switch event.Type {
		case wakeupEventChildDone:
			if slices.Contains(keys, childDoneRunKey(event.ChatID)) {
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
					next = &wakeupDelivery{
						text:   fmt.Sprintf("%s\n\nChild agent %q (%s) is done with status %s.", wakeup.Prompt, child.Title, child.ID, child.Status),
						runKey: childDoneRunKey(child.ID),
					}
					continue
				}
			}
			pending++
		case wakeupEventPullRequest:
			if slices.Contains(keys, pullRequestRunKey(event.URL)) {
				continue
			}
			// At most one read per tick: a later entry waits until next
			// is delivered.
			if next == nil {
				summary, err := p.pullRequestWakeupText(ctx, chat, event)
				if err != nil {
					return p.finishWakeupClaim(ctx, fdb, chat, wakeup, now, p.pullRequestReadFailed(ctx, wakeup, now, event.URL, pending > 0, err))
				}
				if summary != "" {
					next = &wakeupDelivery{
						text:   wakeup.Prompt + "\n\n" + summary,
						runKey: pullRequestRunKey(event.URL),
					}
					continue
				}
			}
			pending++
			pullRequestPending = true
		}
	}
	interval := wakeupPollInterval
	if pullRequestPending {
		interval = pullRequestPollInterval
	}
	if next == nil {
		return p.finishWakeupClaim(ctx, fdb, chat, wakeup, now, eventsWakeupFinish(wakeup, now, 0, pending, interval))
	}
	if paused, err := p.pauseCappedWakeup(ctx, fdb, chat, wakeup, now); paused || err != nil {
		return err
	}
	delivery := *next
	delivery.finish = func(admittedAt time.Time) wakeupFinish {
		return eventsWakeupFinish(wakeup, admittedAt, 1, pending, interval)
	}
	err = p.sendWakeupPrompt(ctx, chat, wakeup, now, delivery)
	// A failed send keeps the entry's own key free, so a later poll retries
	// it.
	failedRunKey := delivery.runKey + ":failed:" + now.UTC().Format(time.RFC3339Nano)
	return p.finishWakeupSend(ctx, fdb, chat, wakeup, now, failedRunKey, err)
}

// eventsWakeupFinish completes an events wake-up when no watched entry still
// needs a run, and otherwise polls again after interval.
func eventsWakeupFinish(wakeup forkdb.ChatWakeup, now time.Time, runDelta int32, pending int, interval time.Duration) wakeupFinish {
	if pending == 0 {
		return wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "done", runDelta: runDelta}
	}
	return eventsWakeupRunAt(wakeup, now, runDelta, now.Add(interval))
}

// eventsWakeupRunAt schedules the next poll of an events wake-up at next. A
// poll that would land after the expiry lands at the expiry instead, so the
// watch covers its whole lifetime, and the wake-up completes once that last
// poll is done or its max_runs are used up.
func eventsWakeupRunAt(wakeup forkdb.ChatWakeup, now time.Time, runDelta int32, next time.Time) wakeupFinish {
	switch {
	case wakeup.MaxRuns.Valid && wakeup.RunCount+runDelta >= wakeup.MaxRuns.Int32:
		return wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "max_runs", runDelta: runDelta}
	case wakeup.ExpiresAt.Valid && !now.Before(wakeup.ExpiresAt.Time):
		return wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "expired", runDelta: runDelta}
	case wakeup.ExpiresAt.Valid && next.After(wakeup.ExpiresAt.Time):
		next = wakeup.ExpiresAt.Time
	}
	return wakeupFinish{status: forkdb.ChatWakeupStatusActive, nextRunAt: sql.NullTime{Time: next, Valid: true}, runDelta: runDelta}
}

// eventsFinalPoll reports whether wakeup is the last poll of an events
// wake-up, which eventsWakeupRunAt schedules at the expiry.
func eventsFinalPoll(wakeup forkdb.ChatWakeup) bool {
	return wakeup.Kind == forkdb.ChatWakeupKindEvents && wakeup.ExpiresAt.Valid && wakeup.NextRunAt.Valid &&
		!wakeup.NextRunAt.Time.Before(wakeup.ExpiresAt.Time)
}

// childDoneRunKey names the run of a child_done event, so each child wakes
// the chat once.
func childDoneRunKey(childID uuid.UUID) string {
	return wakeupEventChildDone + ":" + childID.String()
}

// childDone reports whether a child agent finished its work: it is archived,
// or it settled in a status other than running, interrupting or paused (the
// rule of checkSubagentCompletion), does not wait for events of its own
// children and does not wait for an answer.
func (p *Server) childDone(ctx context.Context, child database.Chat) (bool, error) {
	if child.Archived {
		return true, nil
	}
	switch child.Status {
	case database.ChatStatusRunning, database.ChatStatusInterrupting, database.ChatStatusPaused:
		return false, nil
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return false, err
	}
	wakeups, err := fdb.ListChatWakeupsByChatID(ctx, child.ID)
	if err != nil {
		return false, xerrors.Errorf("list wake-ups of child %s: %w", child.ID, err)
	}
	for _, wakeup := range wakeups {
		if wakeup.Kind == forkdb.ChatWakeupKindEvents && wakeup.Status == forkdb.ChatWakeupStatusActive {
			return false, nil
		}
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
