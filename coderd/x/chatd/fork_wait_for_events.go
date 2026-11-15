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
	ProcessIDs        []string `json:"process_ids,omitempty" description:"IDs of background processes of this chat to watch, the background_process_id that execute returns. The chat gets one message when each one exits, with its exit code and the end of its output. At most 10. Without child_chat_ids, only the processes (and the pull request) are watched."`
	// The output pattern fields apply to every process of the call.
	ProcessOutputPattern         string `json:"process_output_pattern,omitempty" description:"A regular expression (RE2 syntax) for output lines of the processes. While a process runs, the chat also gets a message when it printed new lines that match, with those lines. Requires process_ids."`
	ProcessOutputCooldownSeconds *int   `json:"process_output_cooldown_seconds,omitempty" description:"Wait at least this many seconds after the last message of this wake-up before a message for matching lines, which collects the lines printed meanwhile. 0 to 86400. Defaults to 60. Requires process_output_pattern."`
	ProcessOutputMaxEvents       *int   `json:"process_output_max_events,omitempty" description:"Send at most this many messages for matching lines per process. 1 to 100. Defaults to 10. The exit message comes either way. Requires process_output_pattern."`
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
	Processes   []waitForEventsProcess    `json:"processes,omitempty"`
}

// wakeupEvent is what one watch of an events wake-up waits for, decoded from
// its row by watchEvent. Type names what the worker waits for. A child_done
// entry sets ChatID, and a process_exit entry sets ProcessID, Command and
// AgentID, the workspace agent that runs the process, and with an output
// pattern also the Output fields. A pull_request entry
// sets the other fields: Since is when the watch started, and HeadSHA and
// ChecksState are the baseline at that time. The fields without a column of
// their own are the spec of the row.
type wakeupEvent struct {
	Type        string    `json:"type"`
	ChatID      uuid.UUID `json:"chat_id,omitzero"`
	URL         string    `json:"url,omitempty"`
	On          []string  `json:"on,omitempty"`
	Since       time.Time `json:"since,omitzero"`
	HeadSHA     string    `json:"head_sha,omitempty"`
	ChecksState string    `json:"checks_state,omitempty"`
	// ChecksUpdatedAt is the baseline's CheckSummary.UpdatedAt.
	ChecksUpdatedAt time.Time `json:"checks_updated_at,omitzero"`
	// Seen lists the URLs of reviews and comments that the baseline read
	// listed and that count as the baseline.
	Seen      []string  `json:"seen,omitempty"`
	ProcessID string    `json:"process_id,omitempty"`
	Command   string    `json:"command,omitempty"`
	AgentID   uuid.UUID `json:"agent_id,omitzero"`
	// OutputPattern, OutputCooldownSeconds and OutputMaxEvents are the
	// process_output_* arguments that the call resolved.
	OutputPattern         string `json:"output_pattern,omitempty"`
	OutputCooldownSeconds int    `json:"output_cooldown_seconds,omitempty"`
	OutputMaxEvents       int    `json:"output_max_events,omitempty"`
	// OutputCursor is the output position after the lines that earlier
	// messages of the watch covered.
	OutputCursor int `json:"output_cursor,omitempty"`
}

// watchKinds maps the type of a wakeupEvent to the kind of its watch row.
var watchKinds = map[string]forkdb.ChatWakeupWatchKind{
	wakeupEventChildDone:   forkdb.ChatWakeupWatchKindChildDone,
	wakeupEventProcessExit: forkdb.ChatWakeupWatchKindProcess,
	wakeupEventPullRequest: forkdb.ChatWakeupWatchKindPullRequest,
}

// insertWakeupWatches inserts one watch row of wakeup per event, in order.
func insertWakeupWatches(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, events []wakeupEvent) error {
	for i, event := range events {
		kind, ok := watchKinds[event.Type]
		if !ok {
			return xerrors.Errorf("unknown event type %q", event.Type)
		}
		params := forkdb.InsertChatWakeupWatchParams{
			ID:        uuid.New(),
			WakeupID:  wakeup.ID,
			Kind:      kind,
			Ordinal:   int32(i), //nolint:gosec // A wake-up watches at most 61 entries.
			CreatedAt: wakeup.CreatedAt,
		}
		switch kind {
		case forkdb.ChatWakeupWatchKindChildDone:
			params.ChildChatID = uuid.NullUUID{UUID: event.ChatID, Valid: true}
		case forkdb.ChatWakeupWatchKindProcess:
			params.ProcessID = sql.NullString{String: event.ProcessID, Valid: true}
			params.AgentID = uuid.NullUUID{UUID: event.AgentID, Valid: event.AgentID != uuid.Nil}
		case forkdb.ChatWakeupWatchKindPullRequest:
			params.PullRequestUrl = sql.NullString{String: event.URL, Valid: true}
		}
		// The targets have columns of their own.
		spec := event
		spec.Type, spec.ChatID, spec.ProcessID, spec.AgentID, spec.URL = "", uuid.Nil, "", uuid.Nil, ""
		raw, err := json.Marshal(spec)
		if err != nil {
			return xerrors.Errorf("encode watch spec: %w", err)
		}
		params.Spec = raw
		if _, err := fdb.InsertChatWakeupWatch(ctx, params); err != nil {
			return xerrors.Errorf("insert wake-up watch: %w", err)
		}
	}
	return nil
}

// watchEvent decodes what watch waits for.
func watchEvent(watch forkdb.ChatWakeupWatch) (wakeupEvent, error) {
	var event wakeupEvent
	if err := json.Unmarshal(watch.Spec, &event); err != nil {
		return wakeupEvent{}, xerrors.Errorf("decode spec of watch %s: %w", watch.ID, err)
	}
	switch watch.Kind {
	case forkdb.ChatWakeupWatchKindChildDone:
		event.Type, event.ChatID = wakeupEventChildDone, watch.ChildChatID.UUID
	case forkdb.ChatWakeupWatchKindProcess:
		event.Type, event.ProcessID, event.AgentID = wakeupEventProcessExit, watch.ProcessID.String, watch.AgentID.UUID
	case forkdb.ChatWakeupWatchKindPullRequest:
		event.Type, event.URL = wakeupEventPullRequest, watch.PullRequestUrl.String
	default:
		return wakeupEvent{}, xerrors.Errorf("watch %s has unknown kind %q", watch.ID, watch.Kind)
	}
	return event, nil
}

// waitForEventsTool returns the wait_for_events tool, which creates an events
// wake-up of chat for its child agents.
func (p *Server) waitForEventsTool(chat database.Chat) fantasy.AgentTool {
	return serialWaitForEventsTool{fantasy.NewAgentTool(
		waitForEventsToolName,
		"Wait for child agents without blocking: call this, then end your "+
			"turn. This chat then receives one user message per watched child "+
			"agent when that child is done, so you can read its result with "+
			"wait_agent and continue. Watches child_chat_ids, or by default "+
			"every child agent that is still working, except children that an "+
			"earlier call still watches. A message never "+
			"interrupts a running turn. Stops after expires_in_seconds "+
			"(default 24 hours). Returns the wake-up ID and the watched children. "+
			"With pull_request_url, it also watches that GitHub pull request "+
			"and sends one message when someone else reviews or comments, CI "+
			"settles, or it closes. With process_ids, it watches background "+
			"processes from execute and sends one message per process when it "+
			"exits, with its exit code and the end of its output; a process "+
			"that already exited wakes the chat right after this turn. With "+
			"process_output_pattern, it also sends a message while a process "+
			"runs when it printed new lines that match, at most once per "+
			"cool-down and up to process_output_max_events times per process.",
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
	)}
}

// serialWaitForEventsTool runs the wait_for_events calls of a step one at a
// time, so each call's duplicate guard sees the wake-ups of the calls before
// it.
type serialWaitForEventsTool struct{ fantasy.AgentTool }

func (serialWaitForEventsTool) SerialToolCalls() bool { return true }

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
// pull request, its children, its processes, or several of them, and maps it
// to a wake-up spec. It checks the other arguments before it reads the pull
// request, which can page through its activity on the owner's quota.
func (p *Server) newWaitForEventsSpec(ctx context.Context, chat database.Chat, args waitForEventsArgs) (wakeupSpec, error) {
	watchesPullRequest := strings.TrimSpace(args.PullRequestURL) != ""
	if !watchesPullRequest && len(args.PullRequestEvents) > 0 {
		return wakeupSpec{}, xerrors.New("pull_request_events requires pull_request_url")
	}
	expiresInSeconds, err := waitForEventsExpiry(args)
	if err != nil {
		return wakeupSpec{}, err
	}
	output, err := processOutputArgs(args)
	if err != nil {
		return wakeupSpec{}, err
	}
	var children []database.Chat
	// A call that names only a pull request or processes waits for no
	// children.
	if (!watchesPullRequest && len(args.ProcessIDs) == 0) || len(args.ChildChatIDs) > 0 {
		children, err = p.waitForEventsChildren(ctx, chat, args.ChildChatIDs)
		if err != nil {
			return wakeupSpec{}, err
		}
	}
	var processes []wakeupEvent
	if len(args.ProcessIDs) > 0 {
		processes, err = p.processWaitEvents(ctx, chat, args.ProcessIDs, output)
		if err != nil {
			return wakeupSpec{}, err
		}
	}
	var pullRequest *wakeupEvent
	if watchesPullRequest {
		event, err := p.pullRequestWaitEvent(ctx, chat, args.PullRequestURL, args.PullRequestEvents)
		if err != nil {
			return wakeupSpec{}, err
		}
		pullRequest = &event
	}
	return waitForEventsSpec(args.Prompt, expiresInSeconds, children, processes, pullRequest), nil
}

// waitForEventsResponse describes wakeup with the current state of the
// children it watches and the baseline of its pull request.
func (p *Server) waitForEventsResponse(ctx context.Context, chat database.Chat, wakeup forkdb.ChatWakeup) (waitForEventsResult, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return waitForEventsResult{}, xerrors.Errorf("fork queries: %w", err)
	}
	watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
	if err != nil {
		return waitForEventsResult{}, xerrors.Errorf("list wake-up watches: %w", err)
	}
	events := make([]wakeupEvent, 0, len(watches))
	for _, watch := range watches {
		event, err := watchEvent(watch)
		if err != nil {
			return waitForEventsResult{}, err
		}
		events = append(events, event)
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
		if event.Type == wakeupEventProcessExit {
			result.Processes = append(result.Processes, waitForEventsProcess{ProcessID: event.ProcessID, Command: event.Command, OutputPattern: event.OutputPattern})
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
	rows, waiting, err := p.childrenSnapshot(ctx, chat)
	if err != nil {
		return nil, err
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
			if !childDone(child, waiting) {
				watched = append(watched, child)
			}
		}
	}
	// A child that an active wake-up still watches would otherwise wake
	// the chat once per wake-up.
	watching, err := p.openWatches(ctx, chat)
	if err != nil {
		return nil, err
	}
	var watchedBy []string
	watched = slices.DeleteFunc(watched, func(child database.Chat) bool {
		wakeupID, ok := watching.children[child.ID]
		if ok && !slices.Contains(watchedBy, wakeupID.String()) {
			watchedBy = append(watchedBy, wakeupID.String())
		}
		return ok
	})
	if len(watched) == 0 && len(watchedBy) > 0 {
		return nil, xerrors.Errorf("these child agents are already watched by wake-up %s: end your turn to wait for them", strings.Join(watchedBy, ", "))
	}
	if len(watched) == 0 {
		return nil, xerrors.New("no running child agents to wait for")
	}
	if len(watched) > maxWaitForEventsChildren {
		return nil, xerrors.Errorf("cannot wait for %d child agents at once, the maximum is %d", len(watched), maxWaitForEventsChildren)
	}
	return watched, nil
}

// waitForEventsExpiry returns how many seconds a wait_for_events wake-up
// lasts: expires_in_seconds when set, else the default.
func waitForEventsExpiry(args waitForEventsArgs) (int, error) {
	if args.ExpiresInSeconds == nil {
		return int(defaultWaitForEventsExpiry / time.Second), nil
	}
	maxSeconds := int(maxWakeupPeriod / time.Second)
	minSeconds := int(minWakeupPeriod / time.Second)
	if *args.ExpiresInSeconds < minSeconds || *args.ExpiresInSeconds > maxSeconds {
		return 0, xerrors.Errorf("expires_in_seconds must be between %d and %d, got %d", minSeconds, maxSeconds, *args.ExpiresInSeconds)
	}
	return *args.ExpiresInSeconds, nil
}

// waitForEventsSpec maps a wait_for_events call to the spec of an events
// wake-up with one child_done watch per child, then processes, then
// pullRequest if set.
func waitForEventsSpec(prompt string, expiresInSeconds int, children []database.Chat, processes []wakeupEvent, pullRequest *wakeupEvent) wakeupSpec {
	spec := wakeupSpec{
		source:           wakeupSourceEvents,
		kind:             forkdb.ChatWakeupKindEvents,
		prompt:           prompt,
		expiresInSeconds: expiresInSeconds,
	}
	if strings.TrimSpace(spec.prompt) == "" {
		spec.prompt = defaultWaitForEventsPrompt
		if pullRequest != nil || len(processes) > 0 {
			spec.prompt = defaultPullRequestWaitPrompt
		}
	}
	events := make([]wakeupEvent, 0, len(children)+len(processes))
	for _, child := range children {
		events = append(events, wakeupEvent{Type: wakeupEventChildDone, ChatID: child.ID})
	}
	events = append(events, processes...)
	if pullRequest != nil {
		events = append(events, *pullRequest)
	}
	spec.events = events
	return spec
}

// deliverEventsWakeup sends one message for the first open watch whose child
// is done, process exited or is gone, or pull request has new activity, and
// completes the wake-up once every watch is delivered or gone. deliverWakeup
// calls it only for an idle chat, so the next watch is delivered on a later
// tick, after the turn this one starts.
func (p *Server) deliverEventsWakeup(ctx context.Context, fdb forkdb.Store, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time) error {
	watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
	if err != nil {
		return xerrors.Errorf("list wake-up watches: %w", err)
	}
	// The child_done watches whose child is done, read in one statement, so
	// a child that resumes or starts waiting meanwhile cannot pass for done.
	ready, err := fdb.ListReadyChatWakeupWatches(ctx, forkdb.ListReadyChatWakeupWatchesParams{
		WakeupID:   uuid.NullUUID{UUID: wakeup.ID, Valid: true},
		LimitCount: int32(len(watches)), //nolint:gosec // A wake-up watches at most 61 entries.
	})
	if err != nil {
		return xerrors.Errorf("list ready wake-up watches: %w", err)
	}

	// The watch this tick delivers.
	var next *wakeupDelivery
	// Open watches, other than next, that still need a delivery.
	pending := 0
	// Whether one of them is a pull request, which is polled less often.
	pullRequestPending := false
	// How many of them are processes whose read failed.
	processErrors := 0
	var conns processConns
	defer conns.release()
	// A final poll that runs well after the expiry leaves out the watches
	// whose event may have happened after it. A child's status has no time
	// it became done, so it leaves out the children updated after the
	// expiry: a status change updates the chat, so a child last updated by
	// the expiry was done by then. It leaves out the processes that exited,
	// or whose workspace stopped or was rebuilt, after the expiry. A watch
	// whose send failed happened before the cutoff, so a retry still
	// delivers it.
	lateFinalPoll := eventsFinalPoll(wakeup, now) && now.After(wakeup.ExpiresAt.Time.Add(wakeupPollInterval))
	for _, watch := range watches {
		if watch.DeliveredAt.Valid {
			continue
		}
		event, err := watchEvent(watch)
		if err != nil {
			p.logger.Warn(ctx, "chat wake-up has an invalid watch",
				slog.F("wakeup_id", wakeup.ID),
				slog.Error(err),
			)
			return p.finishWakeup(ctx, chat, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: "invalid_events"})
		}
		switch watch.Kind {
		case forkdb.ChatWakeupWatchKindChildDone:
			done := slices.ContainsFunc(ready, func(r forkdb.ChatWakeupWatch) bool { return r.ID == watch.ID })
			if next == nil && done {
				child, err := p.db.GetChatByID(ctx, event.ChatID)
				if errors.Is(err, sql.ErrNoRows) {
					// A child that is gone has nothing to report.
					continue
				}
				if err != nil {
					return xerrors.Errorf("get child agent: %w", err)
				}
				cutOff := lateFinalPoll && child.UpdatedAt.After(wakeup.ExpiresAt.Time) && !watch.FailedAt.Valid
				if !cutOff {
					next = &wakeupDelivery{
						text:  fmt.Sprintf("%s\n\nChild agent %q (%s) is done with status %s.", wakeup.Prompt, child.Title, child.ID, child.Status),
						watch: &watch,
						done:  true,
					}
					continue
				}
			}
			pending++
		case forkdb.ChatWakeupWatchKindPullRequest:
			// At most one read per tick: a later watch waits until next is
			// delivered.
			if next == nil {
				// A read that may stall starts only while the delivery
				// has time left for it and its message. Otherwise the
				// wake-up stays due for the next pass.
				if !wakeupReadFits(ctx, pullRequestReadTimeout) {
					return nil
				}
				summary, err := p.pullRequestWakeupText(ctx, chat, event, wakeup.ExpiresAt.Time)
				if err != nil {
					return p.finishWakeup(ctx, chat, wakeup, now, p.pullRequestReadFailed(ctx, wakeup, now, event.URL, pending > 0, err))
				}
				if summary != "" {
					next = &wakeupDelivery{
						text:  wakeup.Prompt + "\n\n" + summary,
						watch: &watch,
						done:  true,
					}
					continue
				}
			}
			pending++
			pullRequestPending = true
		case forkdb.ChatWakeupWatchKindProcess:
			if next == nil {
				if !wakeupReadFits(ctx, processReadTimeout) {
					return nil
				}
				var endedBy time.Time
				if lateFinalPoll && !watch.FailedAt.Valid {
					endedBy = wakeup.ExpiresAt.Time
				}
				var lines *processLines
				if due, ok := processLinesDue(event, watch.Deliveries, wakeup, now); ok {
					lines = &due
				}
				message, err := p.processWakeupText(ctx, chat, event, endedBy, lines, &conns)
				if err != nil {
					p.logger.Warn(ctx, "chat wake-up could not read its background process",
						slog.F("wakeup_id", wakeup.ID),
						slog.F("chat_id", wakeup.ChatID),
						slog.F("process_id", event.ProcessID),
						slog.Error(err),
					)
					processErrors++
				}
				if message.text != "" {
					next = &wakeupDelivery{
						text:      wakeup.Prompt + "\n\n" + message.text,
						watch:     &watch,
						done:      message.exited,
						specPatch: message.specPatch,
					}
					// A process that printed matching lines still runs, so
					// it stays pending.
					if message.exited {
						continue
					}
				}
			}
			pending++
		}
	}
	interval := wakeupPollInterval
	switch {
	case pullRequestPending:
		interval = pullRequestPollInterval
	case processErrors > 0 && processErrors == pending:
		interval = processErrorRetry
	}
	if next == nil {
		// A failed read on the final poll could miss an exit by the expiry.
		if processErrors > 0 {
			if retry, ok := eventsFinalRetry(wakeup, now, now.Add(processErrorRetry)); ok {
				return p.finishWakeup(ctx, chat, wakeup, now, retry)
			}
		}
		return p.finishWakeup(ctx, chat, wakeup, now, eventsWakeupFinish(wakeup, now, 0, pending, interval))
	}
	if paused, err := p.pauseCappedWakeup(ctx, fdb, chat, wakeup, now); paused || err != nil {
		return err
	}
	delivery := *next
	delivery.runKey = watchRunKey(*delivery.watch)
	delivery.finish = func(admittedAt time.Time) wakeupFinish {
		return eventsWakeupFinish(wakeup, admittedAt, 1, pending, interval)
	}
	err = p.sendWakeupPrompt(ctx, chat, wakeup, now, delivery)
	return p.finishWakeupSend(ctx, chat, wakeup, now, delivery, err)
}

// eventsWakeupFinish completes an events wake-up when no watched entry still
// needs a run, and otherwise polls again after interval. One tick delivers at
// most one entry, so a final poll that delivers one polls the others once
// more after that turn.
func eventsWakeupFinish(wakeup forkdb.ChatWakeup, now time.Time, runDelta int32, pending int, interval time.Duration) wakeupFinish {
	if pending == 0 {
		return wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "done", runDelta: runDelta}
	}
	next := now.Add(interval)
	finish := eventsWakeupRunAt(wakeup, now, runDelta, next)
	if runDelta > 0 && finish.reason == "expired" {
		if retry, ok := eventsFinalRetry(wakeup, now, next); ok {
			retry.runDelta = runDelta
			return retry
		}
	}
	return finish
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

// eventsFinalPoll reports whether a claim of wakeup at now is the last poll
// of an events wake-up. eventsWakeupRunAt schedules it at the expiry, but any
// claim at or after the expiry counts, so a chat that was busy across the
// poll before still gets one read through the expiry.
func eventsFinalPoll(wakeup forkdb.ChatWakeup, now time.Time) bool {
	return wakeup.Kind == forkdb.ChatWakeupKindEvents && wakeup.ExpiresAt.Valid && !now.Before(wakeup.ExpiresAt.Time)
}

// eventsFinalRetry ends the claim of a failed final poll of wakeup with a
// retry at next, as long as next is within pullRequestFinalRetryWindow after
// the expiry, so a transient failure at the expiry does not lose what
// happened until then.
func eventsFinalRetry(wakeup forkdb.ChatWakeup, now, next time.Time) (wakeupFinish, bool) {
	if !eventsFinalPoll(wakeup, now) || next.After(wakeup.ExpiresAt.Time.Add(pullRequestFinalRetryWindow)) {
		return wakeupFinish{}, false
	}
	return wakeupFinish{status: forkdb.ChatWakeupStatusActive, nextRunAt: sql.NullTime{Time: next, Valid: true}}, true
}

// watchRunKey names the run that the next delivery of watch records, which
// counts it against the wake-up caps. A watch delivers once per count.
func watchRunKey(watch forkdb.ChatWakeupWatch) string {
	return fmt.Sprintf("watch:%s:%d", watch.ID, watch.Deliveries)
}

// failedRunKey names the run that the failed sends of the delivery with
// runKey share, so they leave runKey free for a retry.
func failedRunKey(runKey string) string {
	return runKey + ":failed"
}

// childDone reports whether a child agent finished its work: it is archived,
// or it settled in a status other than running, interrupting or paused (the
// rule of checkSubagentCompletion) and is not in waiting, the children that
// wait for events of their own children or for an answer.
// ListReadyChatWakeupWatches applies the same rule.
func childDone(child database.Chat, waiting map[uuid.UUID]bool) bool {
	if child.Archived {
		return true
	}
	switch child.Status {
	case database.ChatStatusRunning, database.ChatStatusInterrupting, database.ChatStatusPaused:
		return false
	}
	return !waiting[child.ID]
}

// childrenSnapshot returns the children of chat and the ones of them that
// wait for events of their own children or for an answer. It reads both from
// one snapshot: with separate reads, a child that resumes or starts waiting
// in between could pass for done.
func (p *Server) childrenSnapshot(ctx context.Context, chat database.Chat) ([]database.GetChildChatsByParentIDsRow, map[uuid.UUID]bool, error) {
	var (
		rows    []database.GetChildChatsByParentIDsRow
		waiting map[uuid.UUID]bool
	)
	err := forkdb.InTx(p.db, func(tx database.Store, ftx forkdb.Store) error {
		var err error
		rows, err = tx.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
			ParentIds: []uuid.UUID{chat.ID},
		})
		if err != nil {
			return xerrors.Errorf("list child agents: %w", err)
		}
		ids, err := ftx.ListChildChatIDsWithOpenWakeupWatches(ctx, chat.ID)
		if err != nil {
			return xerrors.Errorf("list wake-ups of child agents: %w", err)
		}
		root := chat.ID
		if chat.RootChatID.Valid {
			root = chat.RootChatID.UUID
		}
		questions, err := ftx.ListPendingChatDescendantQuestionsByRoot(ctx, root)
		if err != nil {
			return xerrors.Errorf("list questions of child agents: %w", err)
		}
		waiting = make(map[uuid.UUID]bool, len(ids)+len(questions))
		for _, id := range ids {
			waiting[id] = true
		}
		for _, question := range questions {
			if question.ParentChatID.Valid && question.ParentChatID.UUID == chat.ID {
				waiting[question.ChatID] = true
			}
		}
		return nil
	}, &database.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	return rows, waiting, nil
}

// openWatches maps the children and the processes that an open watch of an
// active wake-up of chat waits for to the ID of that wake-up.
type openWatches struct {
	children  map[uuid.UUID]uuid.UUID
	processes map[string]uuid.UUID
}

// openWatches lists what the active wake-ups of chat still watch. An expired
// wake-up stays active while its chat is busy, and its final poll still
// delivers what happened by the expiry. A new watch leaves out the children
// that are done, so the expired wake-up gives up its children, but a new
// watch takes an exited process too, so the expired wake-up keeps its
// processes.
func (p *Server) openWatches(ctx context.Context, chat database.Chat) (openWatches, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return openWatches{}, xerrors.Errorf("fork queries: %w", err)
	}
	rows, err := fdb.ListOpenChatWakeupWatchesByChatID(ctx, chat.ID)
	if err != nil {
		return openWatches{}, xerrors.Errorf("list open wake-up watches: %w", err)
	}
	now := p.clock.Now()
	watching := openWatches{children: map[uuid.UUID]uuid.UUID{}, processes: map[string]uuid.UUID{}}
	for _, row := range rows {
		watch := row.ChatWakeupWatch
		switch watch.Kind {
		case forkdb.ChatWakeupWatchKindChildDone:
			if row.WakeupExpiresAt.Valid && !row.WakeupExpiresAt.Time.After(now) {
				continue
			}
			watching.children[watch.ChildChatID.UUID] = watch.WakeupID
		case forkdb.ChatWakeupWatchKindProcess:
			watching.processes[watch.ProcessID.String] = watch.WakeupID
		}
	}
	return watching, nil
}
