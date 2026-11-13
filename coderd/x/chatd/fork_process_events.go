package chatd

// This file is fork code: it holds the background process events of the
// wait_for_events tool of the fork-chat-wakeups experiment. The tool checks
// that each process belongs to the chat, and the wake-up worker polls the
// chat's workspace agent until the process exits or is gone.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	wakeupEventProcessExit = "process_exit"
	// processOutputRunKeyPrefix starts the run keys of the messages for
	// matching output lines.
	processOutputRunKeyPrefix = "process_output"

	maxWaitForEventsProcesses = 10
	// processReadTimeout bounds the agent reads of one tick, dial included,
	// well within wakeupLease, so a stalled agent cannot block the worker
	// loop.
	processReadTimeout = 30 * time.Second
	// processErrorRetry is the next poll after a failed read, when no other
	// entry of the wake-up needs a sooner one.
	processErrorRetry = time.Minute
	// maxProcessTailLines and maxProcessTailBytes cap the output that an
	// exit message quotes.
	maxProcessTailLines = 40
	maxProcessTailBytes = 4096

	maxProcessOutputPatternBytes   = 1024
	defaultProcessOutputCooldown   = 60
	maxProcessOutputCooldown       = 86400
	defaultProcessOutputMaxEvents  = 10
	maxProcessOutputMaxEventsLimit = 100
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
// background processes ids for chat, and returns the process_exit events
// that watch them, with the output pattern fields of output. A process that
// already exited is still watched, so the first poll reports it.
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
	watching, err := p.watchedRunKeys(ctx, chat)
	if err != nil {
		return nil, err
	}
	var watchedBy []string
	requested = slices.DeleteFunc(requested, func(id string) bool {
		wakeupID, ok := watching[processExitRunKey(id)]
		if ok && !slices.Contains(watchedBy, wakeupID.String()) {
			watchedBy = append(watchedBy, wakeupID.String())
		}
		return ok
	})
	if len(requested) == 0 {
		return nil, xerrors.Errorf("these processes are already watched by wake-up %s: end your turn to wait for them", strings.Join(watchedBy, ", "))
	}

	ctx, cancel := context.WithTimeout(ctx, processReadTimeout)
	defer cancel()
	conn, release, err := p.processAgentConn(ctx, chat.ID, chat.AgentID.UUID)
	if err != nil {
		return nil, err
	}
	defer release()
	events := make([]wakeupEvent, 0, len(requested))
	for _, id := range requested {
		process, err := conn.ProcessOutput(ctx, id, nil)
		if processNotFound(err) {
			return nil, xerrors.Errorf("process %q is not a process of this chat: pass the background_process_id that execute returned", id)
		}
		if err != nil {
			return nil, xerrors.Errorf("read process %q: %w", id, err)
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

// processExitRunKey names the run of a process_exit event, so each process
// wakes the chat once, whether it exited or its watch was lost.
func processExitRunKey(processID string) string {
	return wakeupEventProcessExit + ":" + processID
}

// processAgentConn dials the workspace agent agentID for the processes of
// the chat chatID. The caller calls the returned release.
func (p *Server) processAgentConn(ctx context.Context, chatID, agentID uuid.UUID) (workspacesdk.AgentConn, func(), error) {
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
	// The agent keys processes by chat.
	conn.SetExtraHeaders(http.Header{workspacesdk.CoderChatIDHeader: {chatID.String()}})
	return conn, release, nil
}

// processNotFound reports whether err is the agent's answer for a process it
// does not know.
func processNotFound(err error) bool {
	sdkErr, ok := errors.AsType[*codersdk.Error](err)
	return ok && sdkErr.StatusCode() == http.StatusNotFound
}

// processConns holds what the process_exit entries of one tick of an events
// wake-up share: one read deadline, the latest build of the chat's workspace
// and its agents, and one connection per agent. release closes the
// connections.
type processConns struct {
	// ctx bounds every read of the tick, so the entries together stay
	// within processReadTimeout.
	ctx    context.Context
	cancel context.CancelFunc
	agents []uuid.UUID
	// builtAt is when the latest build was created, zero without one.
	builtAt time.Time
	read    bool
	conns   map[uuid.UUID]processConn
}

type processConn struct {
	conn    workspacesdk.AgentConn
	release func()
	err     error
}

// readContext returns ctx bounded by processReadTimeout from the first read
// of the tick on.
func (c *processConns) readContext(ctx context.Context) context.Context {
	if c.ctx == nil {
		c.ctx, c.cancel = context.WithTimeout(ctx, processReadTimeout)
	}
	return c.ctx
}

// release closes the connections of the tick.
func (c *processConns) release() {
	for _, conn := range c.conns {
		if conn.release != nil {
			conn.release()
		}
	}
	if c.cancel != nil {
		c.cancel()
	}
}

// agentRuns reports whether agentID is an agent of the latest build of the
// workspace of chat, and that build starts the workspace. It reads the
// workspace once per tick.
func (c *processConns) agentRuns(ctx context.Context, db database.Store, chat database.Chat, agentID uuid.UUID) (bool, error) {
	if !chat.WorkspaceID.Valid {
		return false, nil
	}
	if !c.read {
		build, err := db.GetLatestWorkspaceBuildByWorkspaceID(ctx, chat.WorkspaceID.UUID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, xerrors.Errorf("get latest workspace build: %w", err)
		}
		if err == nil {
			c.builtAt = build.CreatedAt
		}
		if err == nil && build.Transition == database.WorkspaceTransitionStart {
			agents, err := db.GetWorkspaceAgentsInLatestBuildByWorkspaceID(ctx, chat.WorkspaceID.UUID)
			if err != nil {
				return false, xerrors.Errorf("get workspace agents: %w", err)
			}
			for _, agent := range agents {
				c.agents = append(c.agents, agent.ID)
			}
		}
		c.read = true
	}
	return slices.Contains(c.agents, agentID), nil
}

// conn returns the connection of the tick to agentID, dialed on first use.
// A failed dial is not retried within the tick.
func (c *processConns) conn(ctx context.Context, p *Server, chatID, agentID uuid.UUID) (workspacesdk.AgentConn, error) {
	if cached, ok := c.conns[agentID]; ok {
		return cached.conn, cached.err
	}
	conn, release, err := p.processAgentConn(ctx, chatID, agentID)
	if c.conns == nil {
		c.conns = make(map[uuid.UUID]processConn)
	}
	c.conns[agentID] = processConn{conn: conn, release: release, err: err}
	return conn, err
}

// processWakeupText reads the process of event on the workspace agent of
// chat and returns the message for its exit or for a lost watch, with
// processExitRunKey, or "" while it runs. With lines set, a running process
// that printed matching lines returns the message for them and its
// processOutputRunKey instead. With endedBy set, it also returns "" unless
// the process provably ended by endedBy: the agent lists it as exited by
// then, or its workspace stopped or was rebuilt by then. An error is
// transient: the entry polls again later.
func (p *Server) processWakeupText(ctx context.Context, chat database.Chat, event wakeupEvent, endedBy time.Time, lines *processLines, conns *processConns) (string, string, error) {
	exitKey := processExitRunKey(event.ProcessID)
	ctx = conns.readContext(ctx)
	runs, err := conns.agentRuns(ctx, p.db, chat, event.AgentID)
	if err != nil {
		return "", "", err
	}
	if !runs {
		if !endedBy.IsZero() && conns.builtAt.After(endedBy) {
			return "", "", nil
		}
		return processLostText(event, "the workspace stopped or was rebuilt, so the process is gone"), exitKey, nil
	}
	conn, err := conns.conn(ctx, p, chat.ID, event.AgentID)
	if err != nil {
		return "", "", err
	}
	output, err := conn.ProcessOutput(ctx, event.ProcessID, nil)
	if processNotFound(err) {
		// The agent does not tell when it forgot the process.
		if !endedBy.IsZero() {
			return "", "", nil
		}
		return processLostText(event, "the workspace agent no longer knows it (the agent restarted or the process exited more than an hour ago)"), exitKey, nil
	}
	if err != nil {
		return "", "", xerrors.Errorf("read process %q: %w", event.ProcessID, err)
	}
	if output.Running {
		if lines == nil {
			return "", "", nil
		}
		text, runKey := processLinesText(event, output, *lines)
		return text, runKey, nil
	}
	if !endedBy.IsZero() {
		exited, err := processExitedBy(ctx, conn, event.ProcessID, endedBy)
		if err != nil || !exited {
			return "", "", err
		}
	}
	return processExitText(event, output), exitKey, nil
}

// processLines is where the output pattern of a process watch continues:
// cursor is the output position after the lines that earlier messages
// covered, and sent counts those messages.
type processLines struct {
	cursor int
	sent   int
}

// processLinesDue returns where the output pattern of event continues, or
// false when this poll of wakeup at now reports no lines: event has no
// pattern, its line messages are used up, the wake-up is in its cool-down
// after its last message, or this is the final poll, which reports exits
// only.
func processLinesDue(event wakeupEvent, keys []string, wakeup forkdb.ChatWakeup, now time.Time) (processLines, bool) {
	if event.OutputPattern == "" || eventsFinalPoll(wakeup, now) {
		return processLines{}, false
	}
	var lines processLines
	prefix := processOutputRunKeyPrefix + ":" + event.ProcessID + ":"
	for _, key := range keys {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		// The keys of failed sends end in ":failed" and do not count.
		cursor, err := strconv.Atoi(rest)
		if err != nil {
			continue
		}
		lines.sent++
		lines.cursor = max(lines.cursor, cursor)
	}
	if lines.sent >= event.OutputMaxEvents {
		return processLines{}, false
	}
	cooldown := time.Duration(event.OutputCooldownSeconds) * time.Second
	if wakeup.LastRunAt.Valid && now.Before(wakeup.LastRunAt.Time.Add(cooldown)) {
		return processLines{}, false
	}
	return lines, true
}

// processOutputRunKey names the run of the message for the output lines of
// processID up to position end.
func processOutputRunKey(processID string, end int) string {
	return processOutputRunKeyPrefix + ":" + processID + ":" + strconv.Itoa(end)
}

// processLinesText returns the message for the lines of output after
// lines.cursor that match the pattern of event, with its run key, or "" when
// none match.
func processLinesText(event wakeupEvent, output workspacesdk.ProcessOutputResponse, lines processLines) (string, string) {
	pattern, err := regexp.Compile(event.OutputPattern)
	if err != nil {
		// processOutputArgs checked the pattern.
		return "", ""
	}
	newLines, end := processNewLines(output, lines.cursor)
	matched := slices.DeleteFunc(newLines, func(line string) bool { return !pattern.MatchString(line) })
	if len(matched) == 0 {
		return "", ""
	}
	noun := "lines"
	if len(matched) == 1 {
		noun = "line"
	}
	text := fmt.Sprintf("Background process %s (`%s`) is still running and printed %d new %s matching `%s`.", event.ProcessID, event.Command, len(matched), noun, event.OutputPattern)
	if tail, shown := processOutputTail(strings.Join(matched, "\n")); shown > 0 {
		label := "Matching lines"
		if shown < len(matched) {
			label = fmt.Sprintf("Last %d matching lines", shown)
		}
		text += fmt.Sprintf("\n\n%s:\n```\n%s\n```", label, tail)
	}
	if lines.sent+1 >= event.OutputMaxEvents {
		text += "\n\nThis is the last message for matching lines of this process. Its exit still wakes this chat."
	}
	return text, processOutputRunKey(event.ProcessID, end)
}

// processNewLines returns the complete lines of output that end after
// position cursor, and the position after the last of them, or cursor when
// there are none. A position counts the bytes of the whole output of the
// process. The agent keeps the first and the last 16 KiB of it and joins
// them with an omission marker (agentproc.HeadTailBuffer), so once bytes are
// omitted only the lines after the marker can be new, the first of which may
// have lost its start. Lines that the agent no longer keeps are missed.
func processNewLines(output workspacesdk.ProcessOutputResponse, cursor int) ([]string, int) {
	text := output.Output
	total := len(text)
	start := 0
	if truncated := output.Truncated; truncated != nil {
		total = truncated.OriginalBytes
		if truncated.OmittedBytes > 0 {
			marker := fmt.Sprintf("\n\n... [omitted %d bytes] ...\n\n", truncated.OmittedBytes)
			index := strings.Index(text, marker)
			if index < 0 {
				return nil, cursor
			}
			start = index + len(marker)
			newline := strings.IndexByte(text[start:], '\n')
			if newline < 0 {
				return nil, cursor
			}
			start += newline + 1
		}
	}
	// text ends at position total.
	offset := total - len(text)
	var lines []string
	end := cursor
	for start < len(text) {
		newline := strings.IndexByte(text[start:], '\n')
		if newline < 0 {
			// The process may still be writing this line.
			break
		}
		lineEnd := start + newline + 1
		if offset+lineEnd > cursor {
			lines = append(lines, text[start:lineEnd-1])
			end = offset + lineEnd
		}
		start = lineEnd
	}
	return lines, end
}

// processExitedBy reports whether the agent lists processID as exited by t.
// The list holds at most ten processes, running ones first, so it can leave
// out an exited process, which then does not count as exited by t.
func processExitedBy(ctx context.Context, conn workspacesdk.AgentConn, processID string, t time.Time) (bool, error) {
	list, err := conn.ListProcesses(ctx)
	if err != nil {
		return false, xerrors.Errorf("list processes: %w", err)
	}
	for _, info := range list.Processes {
		if info.ID == processID && info.ExitedAt != nil {
			// ExitedAt has whole seconds, so an exit in the second of t
			// may be after t.
			return !time.Unix(*info.ExitedAt+1, 0).After(t), nil
		}
	}
	return false, nil
}

// processLostText tells that the watch of event ended for reason, without the
// process's exit.
func processLostText(event wakeupEvent, reason string) string {
	return fmt.Sprintf("Lost the watch on background process %s (`%s`): %s.", event.ProcessID, event.Command, reason)
}

// processExitText tells how the process of event exited, with the end of its
// output.
func processExitText(event wakeupEvent, output workspacesdk.ProcessOutputResponse) string {
	exit := "exited without an exit code (killed by a signal)."
	// The agent reports -1 for a process killed by a signal.
	if output.ExitCode != nil && *output.ExitCode >= 0 {
		exit = fmt.Sprintf("exited with code %d.", *output.ExitCode)
	}
	text := fmt.Sprintf("Background process %s (`%s`) %s", event.ProcessID, event.Command, exit)
	tail, lines := processOutputTail(output.Output)
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
