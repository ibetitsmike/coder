package workspacesdk

// This file is fork code: it adds the client for the process watch API of
// the agent, which the wait_for_events tool of the fork-chat-wakeups
// experiment uses. Upstream does not have it.

import (
	"context"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/tracing"
)

// ForkWatchProcessRequest registers a watch on a background process of the
// chat in the request's chat header. The agent reports the exit of every
// process of a chat over its Agent API connection. With OutputPattern set,
// it also reports the output lines that match it: the first report comes at
// once and the next ones at most once per OutputCooldownSeconds, until
// coderd answers a report with no open watch that takes more lines. WakeupID
// names the wake-up that watches: a second request with the same WakeupID
// keeps the watch it registered, and each other WakeupID gets its own watch,
// up to a few per process. Without OutputPattern the request only reads the
// process.
type ForkWatchProcessRequest struct {
	WakeupID              uuid.UUID `json:"wakeup_id,omitzero"`
	OutputPattern         string    `json:"output_pattern,omitempty"`
	OutputCooldownSeconds int       `json:"output_cooldown_seconds,omitempty"`
}

// ForkWatchProcessResponse is the state of the process after the watch was
// registered. Known is false when the agent has no such process for the
// chat, and the other fields are then zero. OutputTail is the end of the
// output of an exited process.
type ForkWatchProcessResponse struct {
	Known      bool   `json:"known"`
	Command    string `json:"command,omitempty"`
	Running    bool   `json:"running"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	ExitedAt   *int64 `json:"exited_at,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
}

// ForkProcessEventsConn is implemented by the AgentConn that DialAgent
// returns. Callers type-assert an AgentConn to it.
type ForkProcessEventsConn interface {
	ForkWatchProcess(ctx context.Context, id string, req ForkWatchProcessRequest) (ForkWatchProcessResponse, error)
}

var _ ForkProcessEventsConn = (*agentConn)(nil)

// ForkWatchProcess registers a watch on process id of the chat in the extra
// headers of the connection and returns the state of the process. Agents
// without the process watch API answer with a *codersdk.Error carrying
// status 404.
func (c *agentConn) ForkWatchProcess(ctx context.Context, id string, req ForkWatchProcessRequest) (ForkWatchProcessResponse, error) {
	ctx, span := tracing.StartSpan(ctx)
	defer span.End()

	res, err := c.apiRequest(ctx, http.MethodPost, "/api/v0/processes/"+url.PathEscape(id)+"/watch", req)
	if err != nil {
		return ForkWatchProcessResponse{}, xerrors.Errorf("do request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ForkWatchProcessResponse{}, readAgentError(res)
	}
	var resp ForkWatchProcessResponse
	if err := decodeAgentJSON(res, &resp); err != nil {
		return ForkWatchProcessResponse{}, xerrors.Errorf("decode response body: %w", err)
	}
	return resp, nil
}
