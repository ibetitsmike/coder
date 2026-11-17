package agentapi

// This file is fork code: it records the process events that workspace
// agents report for the wait_for_events tool of the fork-chat-wakeups
// experiment. Upstream does not have it.

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// ForkChatProcessEvents turns on ReportChatProcessEvents, which acks
// without a write while Options.ForkChatProcessEvents is nil. Poke, when
// set, tells the chat daemon of this replica about a chat whose process
// watch fired. The wake-up sweep of every replica finds it otherwise.
type ForkChatProcessEvents struct {
	Poke func(chatID uuid.UUID)
}

// ReportChatProcessEvents fires the open process watches that the events
// of req match and pokes the chats of those watches. Events that match no
// watch change nothing, and with the fork-chat-wakeups experiment off it
// acks without a write. An error makes the agent send the whole batch
// again, which fires the same watches with the same events.
func (a *API) ReportChatProcessEvents(ctx context.Context, req *agentproto.ReportChatProcessEventsRequest) (*agentproto.ReportChatProcessEventsResponse, error) {
	if a.opts.ForkChatProcessEvents == nil || len(req.GetEvents()) == 0 {
		return &agentproto.ReportChatProcessEventsResponse{}, nil
	}
	fdb, err := forkdb.From(a.opts.Database)
	if err != nil {
		return nil, xerrors.Errorf("fork queries: %w", err)
	}
	//nolint:gocritic // The query fires only the watches on this agent.
	ctx = dbauthz.AsChatd(ctx)
	poked := make(map[uuid.UUID]struct{})
	for _, event := range req.GetEvents() {
		chatID, err := uuid.Parse(event.GetChatId())
		if err != nil || event.GetProcessId() == "" {
			a.opts.Log.Debug(ctx, "skip chat process event without chat or process",
				slog.F("chat_id", event.GetChatId()),
				slog.F("process_id", event.GetProcessId()),
			)
			continue
		}
		arg := forkdb.FireChatProcessWatchesParams{
			ChatID:    chatID,
			AgentID:   a.opts.AgentID,
			ProcessID: event.GetProcessId(),
			FiredAt:   dbtime.Time(a.opts.Clock.Now()),
		}
		at := arg.FiredAt
		if event.GetAt() != nil {
			at = dbtime.Time(event.GetAt().AsTime())
		}
		switch kind := event.GetKind().(type) {
		case *agentproto.ReportChatProcessEventsRequest_Event_Exit_:
			arg.Ended, err = forkdb.ChatProcessExitEnded(forkdb.ChatProcessExit{
				Code: int(kind.Exit.GetExitCode()),
				Tail: kind.Exit.GetOutputTail(),
				At:   at,
			})
			if err != nil {
				return nil, err
			}
		case *agentproto.ReportChatProcessEventsRequest_Event_OutputMatch_:
			match := kind.OutputMatch
			if match.GetMatched() <= 0 {
				continue
			}
			if id := match.GetWakeupId(); id != "" {
				wakeupID, err := uuid.Parse(id)
				if err != nil {
					continue
				}
				arg.WakeupID = uuid.NullUUID{UUID: wakeupID, Valid: true}
			}
			arg.Matched = match.GetMatched()
			arg.Lines = strings.Join(match.GetLines(), "\n")
			arg.Last = match.GetLast()
		default:
			continue
		}
		chats, err := fdb.FireChatProcessWatches(ctx, arg)
		if err != nil {
			return nil, xerrors.Errorf("fire chat process watches: %w", err)
		}
		for _, id := range chats {
			poked[id] = struct{}{}
		}
	}
	if poke := a.opts.ForkChatProcessEvents.Poke; poke != nil {
		for id := range poked {
			poke(id)
		}
	}
	return &agentproto.ReportChatProcessEventsResponse{}, nil
}
