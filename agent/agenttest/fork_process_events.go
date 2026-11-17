package agenttest

// This file is fork code: it fakes the Agent API RPC that reports the
// process events of chats. Upstream does not have it.

import (
	"context"

	agentproto "github.com/coder/coder/v2/agent/proto"
)

type forkFakeAgentAPI struct {
	processEvents    []*agentproto.ReportChatProcessEventsRequest
	processEventsErr func(*agentproto.ReportChatProcessEventsRequest) error
}

// ReportChatProcessEvents records req. With an error func set, it returns
// what the func returns for req.
func (f *FakeAgentAPI) ReportChatProcessEvents(_ context.Context, req *agentproto.ReportChatProcessEventsRequest) (*agentproto.ReportChatProcessEventsResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.fork.processEvents = append(f.fork.processEvents, req)
	if f.fork.processEventsErr != nil {
		if err := f.fork.processEventsErr(req); err != nil {
			return nil, err
		}
	}
	return &agentproto.ReportChatProcessEventsResponse{}, nil
}

// ChatProcessEventReports returns every ReportChatProcessEvents request
// received so far, failed ones included.
func (f *FakeAgentAPI) ChatProcessEventReports() []*agentproto.ReportChatProcessEventsRequest {
	f.Lock()
	defer f.Unlock()
	out := make([]*agentproto.ReportChatProcessEventsRequest, len(f.fork.processEvents))
	copy(out, f.fork.processEvents)
	return out
}

// SetChatProcessEventsError makes ReportChatProcessEvents return what fn
// returns for each request. A nil fn acks every request.
func (f *FakeAgentAPI) SetChatProcessEventsError(fn func(*agentproto.ReportChatProcessEventsRequest) error) {
	f.Lock()
	defer f.Unlock()
	f.fork.processEventsErr = fn
}
