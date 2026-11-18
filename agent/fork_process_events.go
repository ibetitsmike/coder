package agent

// This file is fork code: it starts the Agent API routine that reports the
// process events of chats, for the wait_for_events tool of the
// fork-chat-wakeups experiment. Upstream does not have it.

import (
	"context"

	"github.com/coder/coder/v2/agent/proto"
)

// forkStartChatProcessEvents starts the routine that sends the exits and the
// matching output of chat processes to coderd. The events that one
// connection left unacked go out on the next.
func (a *agent) forkStartChatProcessEvents(connMan *apiConnRoutineManager) {
	connMan.startAgentAPI210("report chat process events", gracefulShutdownBehaviorStop,
		func(ctx context.Context, aAPI proto.DRPCAgentClient210) error {
			client, ok := aAPI.(proto.DRPCForkChatProcessEventsClient)
			if !ok {
				return nil
			}
			return a.processAPI.ReportChatProcessEvents(ctx, client)
		})
}
