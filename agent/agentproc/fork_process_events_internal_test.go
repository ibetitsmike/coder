package agentproc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentchat"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// forkEventsClient is a proto.DRPCForkChatProcessEventsClient that hands
// every report to the test and fails the first fail of them, as a lost ack
// does.
type forkEventsClient struct {
	calls chan *proto.ReportChatProcessEventsRequest
	fail  int
}

func newForkEventsClient(fail int) *forkEventsClient {
	return &forkEventsClient{calls: make(chan *proto.ReportChatProcessEventsRequest, 100), fail: fail}
}

// ReportChatProcessEvents runs on the goroutine of the report loop only.
func (c *forkEventsClient) ReportChatProcessEvents(_ context.Context, req *proto.ReportChatProcessEventsRequest) (*proto.ReportChatProcessEventsResponse, error) {
	c.calls <- req
	if c.fail > 0 {
		c.fail--
		return nil, xerrors.New("connection lost before the ack")
	}
	return &proto.ReportChatProcessEventsResponse{}, nil
}

// forkTestAPI returns an API on clock and its handler with the chat
// middleware.
func forkTestAPI(t *testing.T, clock quartz.Clock) (*API, http.Handler) {
	t.Helper()
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}).Leveled(slog.LevelDebug)
	api := NewAPI(logger, agentexec.DefaultExecer, nil, nil, nil, nil, nil)
	api.manager.clock = clock
	t.Cleanup(func() { _ = api.Close() })
	return api, agentchat.Middleware(api.Routes())
}

// forkReport runs the report loop of api with client until the returned
// function stops it.
func forkReport(ctx context.Context, t *testing.T, api *API, client proto.DRPCForkChatProcessEventsClient) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- api.ReportChatProcessEvents(ctx, client) }()
	return func() {
		cancel()
		require.ErrorIs(t, testutil.TryReceive(context.Background(), t, done), context.Canceled)
	}
}

func forkChatHeaders(chatID, toolCallID uuid.UUID) http.Header {
	h := http.Header{workspacesdk.CoderChatIDHeader: {chatID.String()}}
	if toolCallID != uuid.Nil {
		h.Set(workspacesdk.CoderToolCallIDHeader, toolCallID.String())
	}
	return h
}

func forkPost(t *testing.T, handler http.Handler, path string, body any, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitLong)
	data, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(data))
	for k, v := range header {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// forkStart starts command in the background for the tool call toolCallID
// of chat chatID and returns the process.
func forkStart(t *testing.T, api *API, handler http.Handler, chatID, toolCallID uuid.UUID, command string) *process {
	t.Helper()
	w := forkPost(t, handler, "/start", workspacesdk.StartProcessRequest{Command: command, Background: true}, forkChatHeaders(chatID, toolCallID))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp workspacesdk.StartProcessResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	proc, ok := api.manager.get(chatID, resp.ID)
	require.True(t, ok)
	return proc
}

func forkWatch(t *testing.T, handler http.Handler, chatID uuid.UUID, id string, req workspacesdk.ForkWatchProcessRequest) (int, workspacesdk.ForkWatchProcessResponse) {
	t.Helper()
	w := forkPost(t, handler, "/"+id+"/watch", req, forkChatHeaders(chatID, uuid.Nil))
	var resp workspacesdk.ForkWatchProcessResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	}
	return w.Code, resp
}

func requireExitReport(t *testing.T, req *proto.ReportChatProcessEventsRequest, chatID uuid.UUID, processID string, code int32) {
	t.Helper()
	require.Len(t, req.GetEvents(), 1)
	event := req.GetEvents()[0]
	require.Equal(t, chatID.String(), event.GetChatId())
	require.Equal(t, processID, event.GetProcessId())
	require.NotNil(t, event.GetExit(), "want an exit event")
	require.Equal(t, code, event.GetExit().GetExitCode())
}

// A chat process that exits is reported once, and again after a report
// whose ack was lost.
func TestForkReportProcessExit(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	backoff := mClock.Trap().NewTimer("agentproc", "fork", "process-events-backoff")
	defer backoff.Close()
	api, handler := forkTestAPI(t, mClock)
	client := newForkEventsClient(1)
	stop := forkReport(ctx, t, api, client)

	chatID, toolCallID := uuid.New(), uuid.New()
	proc := forkStart(t, api, handler, chatID, toolCallID, "echo bye; exit 3")
	// A repeated start of the same tool call finds the same process and
	// reports nothing more.
	require.Same(t, proc, forkStart(t, api, handler, chatID, toolCallID, "echo bye; exit 3"))
	// Processes without a chat are not reported.
	noChat := forkPost(t, handler, "/start", workspacesdk.StartProcessRequest{Command: "exit 9", Background: true}, nil)
	require.Equal(t, http.StatusOK, noChat.Code)

	first := testutil.TryReceive(ctx, t, client.calls)
	requireExitReport(t, first, chatID, toolCallID.String(), 3)
	require.Equal(t, "bye\n", first.GetEvents()[0].GetExit().GetOutputTail())

	// The ack was lost: the event goes out again after the backoff.
	backoff.MustWait(ctx).MustRelease(ctx)
	mClock.Advance(forkProcessEventsMinBackoff).MustWait(ctx)
	second := testutil.TryReceive(ctx, t, client.calls)
	requireExitReport(t, second, chatID, toolCallID.String(), 3)

	// The second report was acked, so the next exit goes out alone.
	other := uuid.New()
	forkStart(t, api, handler, chatID, other, "exit 4")
	third := testutil.TryReceive(ctx, t, client.calls)
	requireExitReport(t, third, chatID, other.String(), 4)
	stop()
	require.Empty(t, client.calls)
}

// The events that a lost connection left unacked go out on the next one.
func TestForkReportProcessEventsReconnect(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	backoff := mClock.Trap().NewTimer("agentproc", "fork", "process-events-backoff")
	defer backoff.Close()
	api, handler := forkTestAPI(t, mClock)

	lost := newForkEventsClient(1)
	stop := forkReport(ctx, t, api, lost)
	chatID, toolCallID := uuid.New(), uuid.New()
	forkStart(t, api, handler, chatID, toolCallID, "exit 5")
	requireExitReport(t, testutil.TryReceive(ctx, t, lost.calls), chatID, toolCallID.String(), 5)
	backoff.MustWait(ctx).MustRelease(ctx)
	// The connection ends while the loop backs off.
	stop()

	next := newForkEventsClient(0)
	stop = forkReport(ctx, t, api, next)
	defer stop()
	requireExitReport(t, testutil.TryReceive(ctx, t, next.calls), chatID, toolCallID.String(), 5)
}

// An output watch reports the matching lines at once, then at most once
// per cool-down, and stops after its last report.
func TestForkOutputWatch(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	api, handler := forkTestAPI(t, mClock)
	client := newForkEventsClient(0)
	stop := forkReport(ctx, t, api, client)
	defer stop()

	gate := filepath.Join(t.TempDir(), "gate")
	chatID, toolCallID, wakeupID := uuid.New(), uuid.New(), uuid.New()
	proc := forkStart(t, api, handler, chatID, toolCallID, fmt.Sprintf(
		"printf 'a\\nready 1\\nb\\nready 2\\n'; while [ ! -e %q ]; do sleep 0.05; done; echo ready 3; sleep 60", gate))
	waitOutput := func(want string) {
		require.Eventually(t, func() bool {
			output, _ := proc.output()
			return strings.Contains(output, want)
		}, testutil.WaitLong, testutil.IntervalFast)
	}
	waitOutput("ready 2\n")

	code, resp := forkWatch(t, handler, chatID, proc.id, workspacesdk.ForkWatchProcessRequest{
		WakeupID:              wakeupID,
		OutputPattern:         "^ready",
		OutputMaxEvents:       2,
		OutputCooldownSeconds: 10,
	})
	require.Equal(t, http.StatusOK, code)
	require.True(t, resp.Known)
	require.True(t, resp.Running)

	// tick advances the clock to the next read of the watch and returns
	// the report it led to, if any.
	tick := func() (*proto.ReportChatProcessEventsRequest, bool) {
		mClock.Advance(forkOutputWatchInterval).MustWait(ctx)
		select {
		case req := <-client.calls:
			return req, true
		case <-time.After(testutil.IntervalMedium):
			return nil, false
		}
	}
	// The first match is reported at once.
	mClock.Advance(forkOutputWatchInterval).MustWait(ctx)
	first := testutil.TryReceive(ctx, t, client.calls)
	require.Len(t, first.GetEvents(), 1)
	match := first.GetEvents()[0].GetOutputMatch()
	require.NotNil(t, match)
	require.Equal(t, []string{"ready 1", "ready 2"}, match.GetLines())
	require.EqualValues(t, 2, match.GetMatched())
	require.False(t, match.GetLast())
	require.Equal(t, wakeupID.String(), match.GetWakeupId())
	firstAt := first.GetEvents()[0].GetAt().AsTime()

	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	waitOutput("ready 3\n")
	var second *proto.ReportChatProcessEventsRequest
	for range 20 {
		var ok bool
		if second, ok = tick(); ok {
			break
		}
	}
	require.NotNil(t, second, "the second match is reported after the cool-down")
	match = second.GetEvents()[0].GetOutputMatch()
	require.Equal(t, []string{"ready 3"}, match.GetLines())
	require.EqualValues(t, 1, match.GetMatched())
	require.True(t, match.GetLast())
	require.GreaterOrEqual(t, second.GetEvents()[0].GetAt().AsTime().Sub(firstAt), 10*time.Second)

	// The watch ended with its last report.
	require.Eventually(t, func() bool {
		api.fork.mu.Lock()
		defer api.fork.mu.Unlock()
		return len(api.fork.watches) == 0
	}, testutil.WaitLong, testutil.IntervalFast)
}

func TestForkHandleWatchProcess(t *testing.T) {
	t.Parallel()

	t.Run("Unknown", func(t *testing.T) {
		t.Parallel()
		_, handler := forkTestAPI(t, quartz.NewReal())
		code, resp := forkWatch(t, handler, uuid.New(), "nope", workspacesdk.ForkWatchProcessRequest{})
		require.Equal(t, http.StatusOK, code)
		require.False(t, resp.Known)
	})

	t.Run("OtherChat", func(t *testing.T) {
		t.Parallel()
		api, handler := forkTestAPI(t, quartz.NewReal())
		proc := forkStart(t, api, handler, uuid.New(), uuid.New(), "sleep 60")
		code, resp := forkWatch(t, handler, uuid.New(), proc.id, workspacesdk.ForkWatchProcessRequest{})
		require.Equal(t, http.StatusOK, code)
		require.False(t, resp.Known)
	})

	t.Run("Exited", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		api, handler := forkTestAPI(t, quartz.NewReal())
		chatID := uuid.New()
		proc := forkStart(t, api, handler, chatID, uuid.New(), "echo hi; exit 2")
		testutil.TryReceive(ctx, t, proc.done)
		code, resp := forkWatch(t, handler, chatID, proc.id, workspacesdk.ForkWatchProcessRequest{OutputPattern: "hi"})
		require.Equal(t, http.StatusOK, code)
		require.True(t, resp.Known)
		require.False(t, resp.Running)
		require.Equal(t, "echo hi; exit 2", resp.Command)
		require.NotNil(t, resp.ExitCode)
		require.Equal(t, 2, *resp.ExitCode)
		require.NotNil(t, resp.ExitedAt)
		require.Equal(t, "hi\n", resp.OutputTail)
	})

	t.Run("InvalidPattern", func(t *testing.T) {
		t.Parallel()
		_, handler := forkTestAPI(t, quartz.NewReal())
		code, _ := forkWatch(t, handler, uuid.New(), "nope", workspacesdk.ForkWatchProcessRequest{OutputPattern: "("})
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("PatternWithoutChat", func(t *testing.T) {
		t.Parallel()
		_, handler := forkTestAPI(t, quartz.NewReal())
		w := forkPost(t, handler, "/nope/watch", workspacesdk.ForkWatchProcessRequest{OutputPattern: "x"}, nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("SameWakeupWatchesOnce", func(t *testing.T) {
		t.Parallel()
		api, handler := forkTestAPI(t, quartz.NewReal())
		chatID := uuid.New()
		proc := forkStart(t, api, handler, chatID, uuid.New(), "sleep 60")
		req := workspacesdk.ForkWatchProcessRequest{WakeupID: uuid.New(), OutputPattern: "x"}
		forkWatch(t, handler, chatID, proc.id, req)
		api.fork.mu.Lock()
		watch := api.fork.watches[proc]
		api.fork.mu.Unlock()
		require.NotNil(t, watch)
		forkWatch(t, handler, chatID, proc.id, req)
		api.fork.mu.Lock()
		require.Same(t, watch, api.fork.watches[proc])
		api.fork.mu.Unlock()

		// A watch for another wake-up replaces it.
		req.WakeupID = uuid.New()
		forkWatch(t, handler, chatID, proc.id, req)
		api.fork.mu.Lock()
		replaced := api.fork.watches[proc]
		api.fork.mu.Unlock()
		require.NotSame(t, watch, replaced)
		require.Equal(t, req.WakeupID, replaced.wakeupID)
		select {
		case <-watch.stop:
		default:
			t.Fatal("the replaced watch did not stop")
		}
	})
}

func TestForkNewLines(t *testing.T) {
	t.Parallel()
	head := "h1 ERROR\nh2\n"
	tail := "rt line\nERROR t1\nt2\npart"
	marker := "\n\n... [omitted 100 bytes] ...\n\n"
	truncated := &workspacesdk.ProcessTruncation{OriginalBytes: 1000, OmittedBytes: 100}
	// The output with truncated ends at position 1000.
	tailEnd := func(prefix string) int { return 1000 - len(tail) + len(prefix) }
	for _, tc := range []struct {
		name      string
		output    string
		truncated *workspacesdk.ProcessTruncation
		cursor    int
		wantLines []string
		wantEnd   int
	}{
		{name: "Whole", output: "a\nb\nc", wantLines: []string{"a", "b"}, wantEnd: 4},
		{name: "AfterCursor", output: "a\nb\nc\n", cursor: 2, wantLines: []string{"b", "c"}, wantEnd: 6},
		{name: "NothingNew", output: "a\nb\n", cursor: 4, wantEnd: 4},
		{
			// The head is old, and the first tail line lost its start.
			name: "Truncated", output: head + marker + tail, truncated: truncated, cursor: 20,
			wantLines: []string{"ERROR t1", "t2"}, wantEnd: tailEnd("rt line\nERROR t1\nt2\n"),
		},
		{
			name: "TruncatedAfterCursor", output: head + marker + tail, truncated: truncated,
			cursor: tailEnd("rt line\nERROR t1\n"), wantLines: []string{"t2"}, wantEnd: tailEnd("rt line\nERROR t1\nt2\n"),
		},
		{name: "UnknownMarker", output: "a\nb\n", truncated: truncated, cursor: 7, wantEnd: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines, end := forkNewLines(tc.output, tc.truncated, tc.cursor)
			require.Equal(t, tc.wantLines, lines)
			require.Equal(t, tc.wantEnd, end)
		})
	}
}

func TestForkOutputTail(t *testing.T) {
	t.Parallel()
	require.Equal(t, "short\n", forkOutputTail("short\n"))

	long := strings.Repeat("x", forkMaxOutputTailBytes) + "\nlast\n"
	require.Equal(t, "last\n", forkOutputTail(long))

	// The cut falls on a line start.
	lines := strings.Repeat("y", 100) + "\n" + strings.Repeat("z", forkMaxOutputTailBytes)
	require.Equal(t, strings.Repeat("z", forkMaxOutputTailBytes), forkOutputTail(lines))

	// Without a line start in the tail, it starts at a rune start.
	runes := forkOutputTail(strings.Repeat("é", forkMaxOutputTailBytes))
	require.True(t, utf8.ValidString(runes))
	require.LessOrEqual(t, len(runes), forkMaxOutputTailBytes)
	require.Greater(t, len(runes), forkMaxOutputTailBytes-2)
}

func TestForkCutLine(t *testing.T) {
	t.Parallel()
	require.Equal(t, "abc", forkCutLine("abc"))
	cut := forkCutLine(strings.Repeat("é", forkMaxReportedLineBytes))
	require.True(t, utf8.ValidString(cut))
	require.Len(t, cut, forkMaxReportedLineBytes)
	cut = forkCutLine("x" + strings.Repeat("é", forkMaxReportedLineBytes))
	require.True(t, utf8.ValidString(cut))
	require.Len(t, cut, forkMaxReportedLineBytes-1)
}
