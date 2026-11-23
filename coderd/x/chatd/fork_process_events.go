package chatd

// This file is fork code: it holds the background process events of the
// wait_for_events tool of the fork-chat-wakeups experiment. The tool checks
// that each process belongs to the chat and registers its watches with the
// chat's workspace agent. The agent reports exits and matching output lines
// over its Agent API connection (coderd/agentapi/fork_process_events.go),
// which fires the watches, and the wake-up worker delivers what fired. See
// "Background process events" in ARCHITECTURE.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	wakeupEventProcessExit = "process_exit"

	maxWaitForEventsProcesses = 10
	// processWatchTimeout bounds the agent requests of one wait_for_events
	// call, dial included.
	processWatchTimeout = 30 * time.Second
	// processSweepInterval is how often the waker fires the process watches
	// whose workspace stopped or was rebuilt as lost.
	processSweepInterval = 5 * time.Minute
	// processReportMargin is how long after the expiry of a wake-up its
	// agent must last have said that it holds no unacked event, for the
	// final poll to know every exit from before the expiry. It covers the
	// time between the exit and the send of that report. The agent says so
	// every 30 seconds while a chat process runs.
	processReportMargin = 5 * time.Second
	// processFinalReportWindow bounds how long after the expiry the final
	// poll waits for that report.
	processFinalReportWindow = 10 * time.Minute
	// maxProcessTailLines and maxProcessTailBytes cap the output that an
	// exit message quotes.
	maxProcessTailLines = 40
	maxProcessTailBytes = 4096

	maxProcessOutputPatternBytes   = 1024
	defaultProcessOutputCooldown   = 60
	maxProcessOutputCooldown       = 86400
	defaultProcessOutputMaxEvents  = 10
	maxProcessOutputMaxEventsLimit = 100

	processLostStopped   = "the workspace stopped or was rebuilt, so the process is gone"
	processLostRestarted = "the workspace agent restarted, so it no longer runs the process"
	processLostUnknown   = "the workspace agent no longer knows it (the agent restarted or the process exited more than an hour ago)"
	processAgentTooOld   = "the workspace agent of this chat is too old to report process events: restart the workspace to update its agent, or poll the process with schedule_wakeup"
)

type waitForEventsProcess struct {
	ProcessID     string `json:"process_id"`
	Command       string `json:"command"`
	OutputPattern string `json:"output_pattern,omitempty"`
}

// processOutputArgs checks the output pattern arguments of args and returns
// the event fields they set, all zero without a pattern.
func processOutputArgs(args waitForEventsArgs) (wakeupEvent, error) {
	if args.ProcessOutputPattern == "" {
		if args.ProcessOutputCooldownSeconds != nil || args.ProcessOutputMaxEvents != nil {
			return wakeupEvent{}, xerrors.New("process_output_cooldown_seconds and process_output_max_events require process_output_pattern")
		}
		return wakeupEvent{}, nil
	}
	if len(args.ProcessIDs) == 0 {
		return wakeupEvent{}, xerrors.New("process_output_pattern requires process_ids")
	}
	if len(args.ProcessOutputPattern) > maxProcessOutputPatternBytes {
		return wakeupEvent{}, xerrors.Errorf("process_output_pattern must be at most %d bytes", maxProcessOutputPatternBytes)
	}
	if _, err := regexp.Compile(args.ProcessOutputPattern); err != nil {
		return wakeupEvent{}, xerrors.Errorf("invalid process_output_pattern: %w", err)
	}
	event := wakeupEvent{
		OutputPattern:         args.ProcessOutputPattern,
		OutputCooldownSeconds: defaultProcessOutputCooldown,
		OutputMaxEvents:       defaultProcessOutputMaxEvents,
	}
	if seconds := args.ProcessOutputCooldownSeconds; seconds != nil {
		if *seconds < 0 || *seconds > maxProcessOutputCooldown {
			return wakeupEvent{}, xerrors.Errorf("process_output_cooldown_seconds must be between 0 and %d, got %d", maxProcessOutputCooldown, *seconds)
		}
		event.OutputCooldownSeconds = *seconds
	}
	if count := args.ProcessOutputMaxEvents; count != nil {
		if *count < 1 || *count > maxProcessOutputMaxEventsLimit {
			return wakeupEvent{}, xerrors.Errorf("process_output_max_events must be between 1 and %d, got %d", maxProcessOutputMaxEventsLimit, *count)
		}
		event.OutputMaxEvents = *count
	}
	return event, nil
}

// processWaitEvents checks that the workspace agent of chat runs the
// background processes ids for chat and reports process events, and returns
// the process_exit events that watch them, with the output pattern fields of
// output. A process that already exited is still watched:
// registerProcessWatches fires its watch once the wake-up is committed.
func (p *Server) processWaitEvents(ctx context.Context, chat database.Chat, ids []string, output wakeupEvent) ([]wakeupEvent, error) {
	var requested []string
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, xerrors.New("process_ids must not contain an empty ID")
		}
		if !slices.Contains(requested, id) {
			requested = append(requested, id)
		}
	}
	if len(requested) > maxWaitForEventsProcesses {
		return nil, xerrors.Errorf("cannot wait for %d processes at once, the maximum is %d", len(requested), maxWaitForEventsProcesses)
	}
	// The chat of the turn can predate the workspace that the turn bound.
	chat, err := p.db.GetChatByID(ctx, chat.ID)
	if err != nil {
		return nil, xerrors.Errorf("get chat: %w", err)
	}
	if !chat.AgentID.Valid {
		return nil, xerrors.New("this chat has no workspace agent: start the process with execute first")
	}
	// A process that an active wake-up still watches would otherwise wake
	// the chat once per wake-up.
	watching, err := p.openWatches(ctx, chat)
	if err != nil {
		return nil, err
	}
	var watchedBy []string
	requested = slices.DeleteFunc(requested, func(id string) bool {
		wakeupID, ok := watching.processes[id]
		if ok && !slices.Contains(watchedBy, wakeupID.String()) {
			watchedBy = append(watchedBy, wakeupID.String())
		}
		return ok
	})
	if len(requested) == 0 {
		return nil, xerrors.Errorf("these processes are already watched by wake-up %s: end your turn to wait for them", strings.Join(watchedBy, ", "))
	}

	ctx, cancel := context.WithTimeout(ctx, processWatchTimeout)
	defer cancel()
	conn, release, err := p.processAgentConn(ctx, chat.ID, chat.AgentID.UUID)
	if err != nil {
		return nil, err
	}
	defer release()
	events := make([]wakeupEvent, 0, len(requested))
	for _, id := range requested {
		// A request without a pattern only reads the process.
		process, err := conn.ForkWatchProcess(ctx, id, workspacesdk.ForkWatchProcessRequest{})
		if err != nil {
			return nil, xerrors.Errorf("read process %q: %w", id, err)
		}
		if !process.Known {
			return nil, xerrors.Errorf("process %q is not a process of this chat: pass the background_process_id that execute returned", id)
		}
		event := output
		event.Type = wakeupEventProcessExit
		event.ProcessID = id
		event.Command = process.Command
		event.AgentID = chat.AgentID.UUID
		events = append(events, event)
	}
	return events, nil
}

// processAgentConn dials the workspace agent agentID for the processes of
// the chat chatID. It fails with processAgentTooOld when the agent has no
// process watch API. The caller calls the returned release.
func (p *Server) processAgentConn(ctx context.Context, chatID, agentID uuid.UUID) (workspacesdk.ForkProcessEventsConn, func(), error) {
	if p.agentConnFn == nil {
		return nil, nil, xerrors.New("workspace agent connections are not available")
	}
	// A dial to an offline agent waits until its context ends.
	dialCtx, cancel := context.WithTimeout(ctx, p.dialTimeout)
	defer cancel()
	conn, release, err := p.agentConnFn(dialCtx, agentID)
	if err != nil {
		return nil, nil, xerrors.Errorf("connect to workspace agent: %w", err)
	}
	events, ok := conn.(workspacesdk.ForkProcessEventsConn)
	if !ok {
		release()
		return nil, nil, xerrors.New(processAgentTooOld)
	}
	// The agent keys processes by chat.
	conn.SetExtraHeaders(http.Header{workspacesdk.CoderChatIDHeader: {chatID.String()}})
	return processEventsConn{events}, release, nil
}

// processEventsConn maps the answer of an agent without the process watch
// API, a 404 for an unknown route, to processAgentTooOld. An agent with the
// API answers an unknown process with Known false.
type processEventsConn struct {
	workspacesdk.ForkProcessEventsConn
}

func (c processEventsConn) ForkWatchProcess(ctx context.Context, id string, req workspacesdk.ForkWatchProcessRequest) (workspacesdk.ForkWatchProcessResponse, error) {
	resp, err := c.ForkProcessEventsConn.ForkWatchProcess(ctx, id, req)
	if sdkErr, ok := errors.AsType[*codersdk.Error](err); ok && sdkErr.StatusCode() == http.StatusNotFound {
		return workspacesdk.ForkWatchProcessResponse{}, xerrors.New(processAgentTooOld)
	}
	return resp, err
}

// registerProcessWatches registers the open process watches of wakeup with
// the workspace agent of chat, which then reports their output lines, and
// fires the watches whose process already exited or that the agent no
// longer knows. It runs after the wake-up commits, so a process that exits
// before the registration fires its watch here, and one that exits after it
// fires its watch through the report of the agent: an exit is never missed,
// and a watch delivers once. It returns a warning for the processes it could
// not register, or "".
func (p *Server) registerProcessWatches(ctx context.Context, chat database.Chat, wakeup forkdb.ChatWakeup) string {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return fmt.Sprintf("could not register the process watches: %v", err)
	}
	watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
	if err != nil {
		return fmt.Sprintf("could not register the process watches: %v", err)
	}
	ctx, cancel := context.WithTimeout(ctx, processWatchTimeout)
	defer cancel()
	type agentConn struct {
		conn    workspacesdk.ForkProcessEventsConn
		release func()
		err     error
	}
	conns := make(map[uuid.UUID]agentConn)
	defer func() {
		for _, c := range conns {
			if c.release != nil {
				c.release()
			}
		}
	}()
	var failed []string
	fired := false
	for _, watch := range watches {
		if watch.Kind != forkdb.ChatWakeupWatchKindProcess || watch.DeliveredAt.Valid {
			continue
		}
		event, err := watchEvent(watch)
		if err != nil {
			continue
		}
		c, ok := conns[event.AgentID]
		if !ok {
			c.conn, c.release, c.err = p.processAgentConn(ctx, chat.ID, event.AgentID)
			conns[event.AgentID] = c
		}
		ok, err = p.registerProcessWatch(ctx, fdb, c.conn, c.err, wakeup, event)
		if err != nil {
			p.logger.Warn(ctx, "chat wake-up could not register its process watch",
				slog.F("wakeup_id", wakeup.ID),
				slog.F("chat_id", chat.ID),
				slog.F("process_id", event.ProcessID),
				slog.Error(err),
			)
			failed = append(failed, fmt.Sprintf("%s (%v)", event.ProcessID, err))
		}
		fired = fired || ok
	}
	if fired {
		p.forkWaker().poke(chat.ID)
	}
	if len(failed) == 0 {
		return ""
	}
	return fmt.Sprintf("the workspace agent did not confirm the watch on %s. Its exit still wakes this chat if the agent reports it, but an exit before this call and matching output may be missed: check the process with process_output.", strings.Join(failed, ", "))
}

// registerProcessWatch registers the watch of event with conn, or fails
// with dialErr, and fires the watch when the process already exited or the
// agent does not know it. It reports whether it fired the watch.
func (p *Server) registerProcessWatch(ctx context.Context, fdb forkdb.Store, conn workspacesdk.ForkProcessEventsConn, dialErr error, wakeup forkdb.ChatWakeup, event wakeupEvent) (bool, error) {
	if dialErr != nil {
		return false, dialErr
	}
	resp, err := conn.ForkWatchProcess(ctx, event.ProcessID, workspacesdk.ForkWatchProcessRequest{
		WakeupID:              wakeup.ID,
		OutputPattern:         event.OutputPattern,
		OutputCooldownSeconds: event.OutputCooldownSeconds,
	})
	if err != nil {
		return false, err
	}
	now := dbtime.Time(p.clock.Now())
	var ended pqtype.NullRawMessage
	switch {
	case !resp.Known:
		ended, err = forkdb.ChatProcessLostEnded(forkdb.ChatProcessLost{Reason: processLostUnknown, At: now})
	case !resp.Running:
		exit := forkdb.ChatProcessExit{Code: -1, Tail: resp.OutputTail, At: now}
		if resp.ExitCode != nil {
			exit.Code = *resp.ExitCode
		}
		if resp.ExitedAt != nil {
			exit.At = time.Unix(*resp.ExitedAt, 0).UTC()
		}
		ended, err = forkdb.ChatProcessExitEnded(exit)
	default:
		return false, nil
	}
	if err != nil {
		return false, err
	}
	chats, err := fdb.FireChatProcessWatches(ctx, forkdb.FireChatProcessWatchesParams{
		FiredAt:   now,
		Ended:     ended,
		ChatID:    wakeup.ChatID,
		AgentID:   event.AgentID,
		ProcessID: event.ProcessID,
		WakeupID:  uuid.NullUUID{UUID: wakeup.ID, Valid: true},
	})
	if err != nil {
		return false, xerrors.Errorf("fire process watch: %w", err)
	}
	return len(chats) > 0, nil
}

// processReported reports whether the agent of the process watch of event
// said, processReportMargin or more after expiresAt, that it holds no event
// that coderd did not ack, so every exit from before expiresAt fired the
// watch. Only a report that coderd acked records it, so neither a lost
// connection nor a failing report passes for it.
func processReported(event wakeupEvent, expiresAt time.Time) bool {
	return !event.ReportedAt.Before(expiresAt.Add(processReportMargin))
}

// sweepProcessWatches fires as lost the process watches whose workspace
// stopped or was rebuilt, or whose agent restarted after the watch began,
// which no agent reports anything for any more, and pokes their chats.
func (k *forkWaker) sweepProcessWatches(ctx context.Context, fdb forkdb.Store) {
	chats, err := fdb.MarkLostChatProcessWatches(ctx, forkdb.MarkLostChatProcessWatchesParams{
		Now:             dbtime.Time(k.clock.Now("chatd", "waker")),
		Reason:          processLostStopped,
		RestartedReason: processLostRestarted,
	})
	if err != nil {
		if ctx.Err() == nil {
			k.logger.Warn(ctx, "chat waker could not sweep process watches", slogError(err))
		}
		return
	}
	for _, chatID := range chats {
		k.poke(chatID)
	}
}

// processWatchEnded reports whether an exit or a loss fired watch.
func processWatchEnded(watch forkdb.ChatWakeupWatch) bool {
	if !watch.FiredAt.Valid {
		return false
	}
	var fired forkdb.ChatProcessFired
	if err := json.Unmarshal(watch.FiredPayload.RawMessage, &fired); err != nil {
		return true
	}
	return fired.Exit != nil || fired.Lost != nil
}

// processLinesCooldown returns the cool-down of the process watch watch,
// whose event is event, when it may still deliver output lines: it has a
// pattern and a cool-down, its line messages are not used up, and no exit or
// loss fired it.
func processLinesCooldown(watch forkdb.ChatWakeupWatch, event wakeupEvent) (time.Duration, bool) {
	if event.OutputPattern == "" || event.OutputCooldownSeconds <= 0 ||
		int(watch.Deliveries) >= event.OutputMaxEvents || processWatchEnded(watch) {
		return 0, false
	}
	return time.Duration(event.OutputCooldownSeconds) * time.Second, true
}

// processLinesCooling reports whether the process watch watch of wakeup holds
// output lines at now that wait for the cool-down after the last message of
// wakeup. ListDueChatWakeups applies the same rule, so such a watch does not
// make its wake-up due.
func processLinesCooling(wakeup forkdb.ChatWakeup, watch forkdb.ChatWakeupWatch, event wakeupEvent, now time.Time) bool {
	cooldown, ok := processLinesCooldown(watch, event)
	return ok && wakeup.LastRunAt.Valid && now.Before(wakeup.LastRunAt.Time.Add(cooldown))
}

// processFiredText returns the message for what fired the process watch
// watch of wakeup, whose event is event, and whether it ends the watch, or
// "" when the pass at now delivers nothing for it. An exit or a loss ends
// the watch and folds in the matching lines the payload held when it fired,
// which a failed send, a cool-down or a busy chat held back. Such a folded
// batch is not a message of its own: it does not append the last-message
// text of the lines, and the final poll delivers it, since the lines were
// collected before the event that ends the watch. A late final poll
// (lateFinalPoll) leaves out an exit or a loss after the expiry, unless an
// earlier send of it failed.
func processFiredText(wakeup forkdb.ChatWakeup, watch forkdb.ChatWakeupWatch, event wakeupEvent, now time.Time, lateFinalPoll bool) (string, bool) {
	var fired forkdb.ChatProcessFired
	if err := json.Unmarshal(watch.FiredPayload.RawMessage, &fired); err != nil {
		return processLostText(event, "its event could not be read"), true
	}
	cutOff := func(at time.Time) bool {
		return lateFinalPoll && !watch.FailedAt.Valid && at.After(wakeup.ExpiresAt.Time)
	}
	switch {
	case fired.Exit != nil:
		// The agent reports the exit time in whole seconds, so an exit in
		// the second of the expiry may be after it.
		if cutOff(fired.Exit.At.Add(time.Second)) {
			return "", false
		}
		text := processExitText(event, fired.Exit.Code, fired.Exit.Tail)
		if folded := processFoldedLinesText(event, fired.Matched, fired.Lines, true); folded != "" {
			text += "\n\n" + folded
		}
		return text, true
	case fired.Lost != nil:
		if cutOff(fired.Lost.At) {
			return "", false
		}
		text := processLostText(event, fired.Lost.Reason)
		if folded := processFoldedLinesText(event, fired.Matched, fired.Lines, false); folded != "" {
			text += "\n\n" + folded
		}
		return text, true
	case fired.Matched > 0 && !eventsFinalPoll(wakeup, now):
		text := processLinesText(event, fired.Matched, fired.Lines)
		if int(watch.Deliveries)+1 >= event.OutputMaxEvents {
			text += processLinesLastText
		}
		return text, false
	}
	return "", false
}

// processFoldedLinesText returns the part of an exit or loss message that
// carries the matching lines the payload held when the exit or loss merged
// in, or "" without any. It is one delivery either way, so the folded batch
// respects the per-watch output_max_events the same way the lines messages
// that led to it did: FireChatProcessWatches takes lines only below the
// count, so a payload that holds lines brings at most one more delivery.
func processFoldedLinesText(event wakeupEvent, matched int, lines string, exited bool) string {
	if matched <= 0 {
		return ""
	}
	noun := "lines"
	if matched == 1 {
		noun = "line"
	}
	when := "Before the watch was lost, it"
	if exited {
		when = "Before it exited, it"
	}
	text := fmt.Sprintf("%s printed %d new %s matching `%s`.", when, matched, noun, event.OutputPattern)
	if tail, shown := processOutputTail(lines); shown > 0 {
		label := "Matching lines"
		if shown < matched {
			label = fmt.Sprintf("Last %d matching lines", shown)
		}
		text += fmt.Sprintf("\n\n%s:\n```\n%s\n```", label, tail)
	}
	return text
}

// processLinesText returns the message for matched output lines of the
// process of event that matched its pattern, the last of which are lines,
// joined by newlines.
func processLinesText(event wakeupEvent, matched int, lines string) string {
	noun := "lines"
	if matched == 1 {
		noun = "line"
	}
	text := fmt.Sprintf("Background process %s (`%s`) is still running and printed %d new %s matching `%s`.", event.ProcessID, event.Command, matched, noun, event.OutputPattern)
	if tail, shown := processOutputTail(lines); shown > 0 {
		label := "Matching lines"
		if shown < matched {
			label = fmt.Sprintf("Last %d matching lines", shown)
		}
		text += fmt.Sprintf("\n\n%s:\n```\n%s\n```", label, tail)
	}
	return text
}

// processLinesLastText ends the last message for matching lines of a
// process.
const processLinesLastText = "\n\nThis is the last message for matching lines of this process. Its exit still wakes this chat."

// processLostText tells that the watch of event ended for reason, without the
// process's exit.
func processLostText(event wakeupEvent, reason string) string {
	return fmt.Sprintf("Lost the watch on background process %s (`%s`): %s.", event.ProcessID, event.Command, reason)
}

// processExitText tells how the process of event exited, with code, -1 for
// a process killed by a signal, and the end of its output, tail.
func processExitText(event wakeupEvent, code int, tail string) string {
	exit := "exited without an exit code (killed by a signal)."
	if code >= 0 {
		exit = fmt.Sprintf("exited with code %d.", code)
	}
	text := fmt.Sprintf("Background process %s (`%s`) %s", event.ProcessID, event.Command, exit)
	tail, lines := processOutputTail(tail)
	if lines == 0 {
		return text
	}
	return fmt.Sprintf("%s\n\nLast %d lines of output:\n```\n%s\n```", text, lines, tail)
}

// processOutputTail returns the last maxProcessTailLines lines of output, at
// most maxProcessTailBytes, without the trailing newline, and how many lines
// that is. It cuts at a line start, unless the last line alone is longer,
// which keeps its last maxProcessTailBytes bytes from a rune start.
func processOutputTail(output string) (string, int) {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return "", 0
	}
	lines := strings.Split(output, "\n")
	if len(lines) > maxProcessTailLines {
		lines = lines[len(lines)-maxProcessTailLines:]
	}
	tail := strings.Join(lines, "\n")
	if len(tail) > maxProcessTailBytes {
		start := len(tail) - maxProcessTailBytes
		if tail[start-1] != '\n' {
			if newline := strings.IndexByte(tail[start:], '\n'); newline >= 0 {
				start += newline + 1
			} else {
				for start < len(tail) && !utf8.RuneStart(tail[start]) {
					start++
				}
			}
		}
		tail = tail[start:]
	}
	return tail, strings.Count(tail, "\n") + 1
}
