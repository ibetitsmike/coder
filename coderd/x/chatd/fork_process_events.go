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
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	wakeupEventProcessExit = "process_exit"

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
)

type waitForEventsProcess struct {
	ProcessID string `json:"process_id"`
	Command   string `json:"command"`
}

// processWaitEvents checks that the workspace agent of chat runs the
// background processes ids for chat, and returns the process_exit events
// that watch them. A process that already exited is still watched, so the
// first poll reports it.
func (p *Server) processWaitEvents(ctx context.Context, chat database.Chat, ids []string) ([]wakeupEvent, error) {
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
		output, err := conn.ProcessOutput(ctx, id, nil)
		if processNotFound(err) {
			return nil, xerrors.Errorf("process %q is not a process of this chat: pass the background_process_id that execute returned", id)
		}
		if err != nil {
			return nil, xerrors.Errorf("read process %q: %w", id, err)
		}
		events = append(events, wakeupEvent{
			Type:      wakeupEventProcessExit,
			ProcessID: id,
			Command:   output.Command,
			AgentID:   chat.AgentID.UUID,
		})
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
// chat and returns the message for its exit or for a lost watch, or "" while
// it runs. With endedBy set, it also returns "" for a process that the agent
// lists as exited after endedBy, or whose workspace stopped or was rebuilt
// after endedBy. An error is transient: the entry polls again later.
func (p *Server) processWakeupText(ctx context.Context, chat database.Chat, event wakeupEvent, endedBy time.Time, conns *processConns) (string, error) {
	ctx = conns.readContext(ctx)
	runs, err := conns.agentRuns(ctx, p.db, chat, event.AgentID)
	if err != nil {
		return "", err
	}
	if !runs {
		if !endedBy.IsZero() && conns.builtAt.After(endedBy) {
			return "", nil
		}
		return processLostText(event, "the workspace stopped or was rebuilt, so the process is gone"), nil
	}
	conn, err := conns.conn(ctx, p, chat.ID, event.AgentID)
	if err != nil {
		return "", err
	}
	output, err := conn.ProcessOutput(ctx, event.ProcessID, nil)
	if processNotFound(err) {
		return processLostText(event, "the workspace agent no longer knows it (the agent restarted or the process exited more than an hour ago)"), nil
	}
	if err != nil {
		return "", xerrors.Errorf("read process %q: %w", event.ProcessID, err)
	}
	if output.Running {
		return "", nil
	}
	if !endedBy.IsZero() && processExitedAfter(ctx, conn, event.ProcessID, endedBy) {
		return "", nil
	}
	return processExitText(event, output), nil
}

// processExitedAfter reports whether the agent lists processID as exited
// after t. A process that the list leaves out, or a failed list, counts as
// exited by t, so its exit is still reported.
func processExitedAfter(ctx context.Context, conn workspacesdk.AgentConn, processID string, t time.Time) bool {
	list, err := conn.ListProcesses(ctx)
	if err != nil {
		return false
	}
	for _, info := range list.Processes {
		if info.ID == processID && info.ExitedAt != nil {
			return time.Unix(*info.ExitedAt, 0).After(t)
		}
	}
	return false
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
	if output.ExitCode != nil {
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
