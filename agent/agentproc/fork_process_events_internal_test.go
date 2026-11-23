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
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

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
// does. The response to the nth report carries the acked_at forkAckedAt(n).
// While closeOutput is set, it names every output event as closed.
type forkEventsClient struct {
	calls       chan *proto.ReportChatProcessEventsRequest
	fail        int
	n           int
	closeOutput atomic.Bool
}

func newForkEventsClient(fail int) *forkEventsClient {
	return &forkEventsClient{calls: make(chan *proto.ReportChatProcessEventsRequest, 100), fail: fail}
}

func forkAckedAt(n int) *timestamppb.Timestamp {
	return timestamppb.New(time.Unix(int64(1000+n), 0))
}

// ReportChatProcessEvents runs on the goroutine of the report loop only.
func (c *forkEventsClient) ReportChatProcessEvents(_ context.Context, req *proto.ReportChatProcessEventsRequest) (*proto.ReportChatProcessEventsResponse, error) {
	c.n++
	c.calls <- req
	if c.fail > 0 {
		c.fail--
		return nil, xerrors.New("connection lost before the ack")
	}
	resp := &proto.ReportChatProcessEventsResponse{AckedAt: forkAckedAt(c.n)}
	if c.closeOutput.Load() {
		for i, event := range req.GetEvents() {
			if event.GetOutputMatch() != nil {
				resp.ClosedOutputEvents = append(resp.ClosedOutputEvents, int32(i)) //nolint:gosec // A test sends few events.
			}
		}
	}
	return resp, nil
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

// While a chat process runs, the agent sends reports without events on
// every heartbeat, after the events that wait for an ack, and none while
// no chat process runs. Each sends back the acked_at of the response before
// it, so coderd certifies only a time that the agent read its queue after.
func TestForkReportProcessEventsHeartbeat(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	heartbeat := mClock.Trap().NewTicker("agentproc", "fork", "process-events-heartbeat")
	defer heartbeat.Close()
	backoff := mClock.Trap().NewTimer("agentproc", "fork", "process-events-backoff")
	defer backoff.Close()
	api, handler := forkTestAPI(t, mClock)
	client := newForkEventsClient(1)
	stop := forkReport(ctx, t, api, client)
	heartbeat.MustWait(ctx).MustRelease(ctx)
	noReport := func() {
		t.Helper()
		select {
		case req := <-client.calls:
			t.Fatalf("unexpected report %v", req)
		case <-time.After(testutil.IntervalMedium):
		}
	}

	// No chat process runs, so a heartbeat sends nothing.
	mClock.Advance(forkProcessEventsHeartbeat).MustWait(ctx)
	noReport()

	chatID := uuid.New()
	forkStart(t, api, handler, chatID, uuid.New(), "sleep 60")
	mClock.Advance(forkProcessEventsHeartbeat - forkProcessEventsMinBackoff).MustWait(ctx)
	noReport()
	// An exit whose ack is lost waits for the backoff, which ends with the
	// next heartbeat.
	exited := uuid.New()
	forkStart(t, api, handler, chatID, exited, "exit 7")
	failed := testutil.TryReceive(ctx, t, client.calls)
	requireExitReport(t, failed, chatID, exited.String(), 7)
	require.Nil(t, failed.GetLastAckedAt(), "nothing was acked yet")
	backoff.MustWait(ctx).MustRelease(ctx)
	mClock.Advance(forkProcessEventsMinBackoff).MustWait(ctx)
	// The exit goes out before the reports without events. The second
	// report was the first acked one.
	requireExitReport(t, testutil.TryReceive(ctx, t, client.calls), chatID, exited.String(), 7)
	empty := func(lastAcked int) {
		t.Helper()
		req := testutil.TryReceive(ctx, t, client.calls)
		require.Empty(t, req.GetEvents())
		require.Equal(t, forkAckedAt(lastAcked).AsTime(), req.GetLastAckedAt().AsTime())
	}
	empty(2)
	empty(3)
	noReport()

	// The next heartbeat sends two more: the first with the acked_at of
	// the last heartbeat, the second with a fresh one.
	mClock.Advance(forkProcessEventsHeartbeat).MustWait(ctx)
	empty(4)
	empty(5)
	noReport()
	stop()
	require.Empty(t, client.calls)
}

// An output watch reports the matching lines at once, then at most once
// per cool-down, and stops when coderd says that no watch takes them.
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
		"printf 'a\\nready 1\\nb\\nready 2\\n'; while [ ! -e %q ]; do sleep 0.05; done; echo ready 3; "+
			"while [ ! -e %q ]; do sleep 0.05; done; echo ready 4; sleep 60", gate, gate+"2"))
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
	require.Equal(t, wakeupID.String(), match.GetWakeupId())
	firstAt := first.GetEvents()[0].GetAt().AsTime()

	// next opens gate and returns the report of the line it lets out,
	// which waits for the cool-down.
	next := func(gate, line string) *proto.ReportChatProcessEventsRequest {
		t.Helper()
		require.NoError(t, os.WriteFile(gate, nil, 0o600))
		waitOutput(line + "\n")
		for range 20 {
			if req, ok := tick(); ok {
				match := req.GetEvents()[0].GetOutputMatch()
				require.Equal(t, []string{line}, match.GetLines())
				require.EqualValues(t, 1, match.GetMatched())
				return req
			}
		}
		t.Fatalf("%q is not reported after the cool-down", line)
		return nil
	}
	watchCount := func() int {
		api.fork.mu.Lock()
		defer api.fork.mu.Unlock()
		return len(api.fork.watches[proc])
	}
	second := next(gate, "ready 3")
	secondAt := second.GetEvents()[0].GetAt().AsTime()
	require.GreaterOrEqual(t, secondAt.Sub(firstAt), 10*time.Second)
	// coderd did not close the watch, so it goes on after any number of
	// reports: coderd counts the messages, not the reports.
	require.Equal(t, 1, watchCount())

	client.closeOutput.Store(true)
	third := next(gate+"2", "ready 4")
	require.True(t, third.GetEvents()[0].GetAt().AsTime().After(secondAt))

	// coderd named the event closed, so the watch ended.
	require.Eventually(t, func() bool {
		api.fork.mu.Lock()
		defer api.fork.mu.Unlock()
		return len(api.fork.watches) == 0
	}, testutil.WaitLong, testutil.IntervalFast)
}

// A line that is still open when the output passes the head of the buffer
// is not reported: the separator that Output puts between a contiguous head
// and tail is not a line end.
func TestForkOutputWatchOpenLineAcrossHead(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	api, handler := forkTestAPI(t, mClock)
	client := newForkEventsClient(0)
	stop := forkReport(ctx, t, api, client)
	defer stop()

	gate := filepath.Join(t.TempDir(), "gate")
	chatID := uuid.New()
	long := MaxHeadBytes + 1000
	proc := forkStart(t, api, handler, chatID, uuid.New(), fmt.Sprintf(
		"printf ERROR; head -c %d /dev/zero | tr '\\000' x; while [ ! -e %q ]; do sleep 0.05; done; printf '\\nERROR done\\n'; sleep 60", long, gate))
	require.Eventually(t, func() bool {
		_, _, total := proc.buf.forkRaw()
		return total == len("ERROR")+long
	}, testutil.WaitLong, testutil.IntervalFast)
	_, truncated := proc.output()
	require.NotNil(t, truncated)
	require.Zero(t, truncated.OmittedBytes, "the head and the tail are contiguous")

	code, _ := forkWatch(t, handler, chatID, proc.id, workspacesdk.ForkWatchProcessRequest{
		WakeupID: uuid.New(), OutputPattern: "^ERROR",
	})
	require.Equal(t, http.StatusOK, code)
	for range 3 {
		mClock.Advance(forkOutputWatchInterval).MustWait(ctx)
		select {
		case req := <-client.calls:
			t.Fatalf("unexpected report of an open line: %v", req.GetEvents()[0].GetOutputMatch().GetLines())
		case <-time.After(testutil.IntervalMedium):
		}
	}

	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	require.Eventually(t, func() bool {
		output, _ := proc.output()
		return strings.HasSuffix(output, "ERROR done\n")
	}, testutil.WaitLong, testutil.IntervalFast)
	mClock.Advance(forkOutputWatchInterval).MustWait(ctx)
	req := testutil.TryReceive(ctx, t, client.calls)
	match := req.GetEvents()[0].GetOutputMatch()
	require.NotNil(t, match)
	require.EqualValues(t, 2, match.GetMatched())
	require.Len(t, match.GetLines(), 2)
	require.Equal(t, "ERROR"+strings.Repeat("x", forkMaxReportedLineBytes-len("ERROR")), match.GetLines()[0])
	require.Equal(t, "ERROR done", match.GetLines()[1])
}

// A new process with the ID of one that the manager forgot drops the queued
// events of the old one, so they cannot fire the watches of the new one.
func TestForkProcessIDReuse(t *testing.T) {
	t.Parallel()
	api, handler := forkTestAPI(t, quartz.NewReal())
	chatID, otherChat, toolCallID := uuid.New(), uuid.New(), uuid.New()
	stale := func(chatID uuid.UUID) *proto.ReportChatProcessEventsRequest_Event {
		return &proto.ReportChatProcessEventsRequest_Event{
			ChatId:    chatID.String(),
			ProcessId: toolCallID.String(),
			Kind:      &proto.ReportChatProcessEventsRequest_Event_Exit_{Exit: &proto.ReportChatProcessEventsRequest_Event_Exit{ExitCode: 1}},
		}
	}
	api.forkEnqueue(stale(chatID))
	kept := stale(otherChat)
	api.forkEnqueue(kept)

	forkStart(t, api, handler, chatID, toolCallID, "sleep 60")
	api.fork.mu.Lock()
	defer api.fork.mu.Unlock()
	require.Equal(t, []*proto.ReportChatProcessEventsRequest_Event{kept}, api.fork.pending)
}

func TestForkReportTime(t *testing.T) {
	t.Parallel()
	last := time.Unix(100, 0)
	require.Equal(t, last.Add(time.Second), forkReportTime(last.Add(time.Second), last))
	require.Equal(t, last.Add(time.Microsecond), forkReportTime(last, last))
	require.Equal(t, last.Add(time.Microsecond), forkReportTime(last.Add(-time.Hour), last), "the clock went back")
	require.Equal(t, last, forkReportTime(last, time.Time{}))
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
		watches := func() []*forkOutputWatch {
			api.fork.mu.Lock()
			defer api.fork.mu.Unlock()
			return slices.Clone(api.fork.watches[proc])
		}
		req := workspacesdk.ForkWatchProcessRequest{WakeupID: uuid.New(), OutputPattern: "x"}
		forkWatch(t, handler, chatID, proc.id, req)
		first := watches()
		require.Len(t, first, 1)
		forkWatch(t, handler, chatID, proc.id, req)
		require.Equal(t, first, watches())

		// A watch for another wake-up runs beside it, so a paused
		// wake-up that resumes still gets its lines.
		for i := 1; i < forkMaxOutputWatches; i++ {
			req.WakeupID = uuid.New()
			forkWatch(t, handler, chatID, proc.id, req)
		}
		got := watches()
		require.Len(t, got, forkMaxOutputWatches)
		require.Same(t, first[0], got[0])
		select {
		case <-first[0].stop:
			t.Fatal("the watch of the first wake-up stopped")
		default:
		}

		// Past the cap the oldest watch stops.
		req.WakeupID = uuid.New()
		forkWatch(t, handler, chatID, proc.id, req)
		got = watches()
		require.Len(t, got, forkMaxOutputWatches)
		require.NotContains(t, got, first[0])
		require.Equal(t, req.WakeupID, got[len(got)-1].wakeupID)
		select {
		case <-first[0].stop:
		default:
			t.Fatal("the oldest watch did not stop")
		}
	})
}

func TestForkNewLines(t *testing.T) {
	t.Parallel()
	head := "h1 ERROR\nh2\n"
	tail := "rt line\nERROR t1\nt2\npart"
	// With bytes omitted the output ends at position 1000.
	const total = 1000
	tailEnd := func(prefix string) int { return total - len(tail) + len(prefix) }
	for _, tc := range []struct {
		name       string
		head, tail string
		total      int
		cursor     int
		wantLines  []string
		wantEnd    int
	}{
		{name: "Whole", head: "a\nb\nc", total: 5, wantLines: []string{"a", "b"}, wantEnd: 4},
		{name: "AfterCursor", head: "a\nb\nc\n", total: 6, cursor: 2, wantLines: []string{"b", "c"}, wantEnd: 6},
		{name: "NothingNew", head: "a\nb\n", total: 4, cursor: 4, wantEnd: 4},
		{
			// The head and the tail are contiguous, so a line that
			// spans them is one line, not two.
			name: "Contiguous", head: "h1\nERROR pa", tail: "rt\nt2\nERROR still", total: 28, cursor: 3,
			wantLines: []string{"ERROR part", "t2"}, wantEnd: 17,
		},
		{
			name: "ContiguousOpenLine", head: "h1\nERROR pa", tail: "rt still", total: 19, cursor: 3,
			wantEnd: 3,
		},
		{
			// The head is old, and the first tail line lost its start.
			name: "Truncated", head: head, tail: tail, total: total, cursor: 20,
			wantLines: []string{"ERROR t1", "t2"}, wantEnd: tailEnd("rt line\nERROR t1\nt2\n"),
		},
		{
			name: "TruncatedAfterCursor", head: head, tail: tail, total: total,
			cursor: tailEnd("rt line\nERROR t1\n"), wantLines: []string{"t2"}, wantEnd: tailEnd("rt line\nERROR t1\nt2\n"),
		},
		{name: "TruncatedOneLine", head: head, tail: "no line end", total: total, cursor: 7, wantEnd: 7},
		{name: "CRLF", head: "READY\r\nsecond\r\n", total: 14, wantLines: []string{"READY", "second"}, wantEnd: 14},
		{name: "MixedLineEnds", head: "lf\nCRLF\r\ncrlf\r\nlf2\n", total: 18,
			wantLines: []string{"lf", "CRLF", "crlf", "lf2"}, wantEnd: 18},
		{name: "CRLFPosition", head: "READY\r\n", total: 7, cursor: 7, wantEnd: 7},
		{
			// A bare CR does not end a line, so the CR stays in the text.
			name: "BareCRKept", head: "a\rb\n", total: 4, wantLines: []string{"a\rb"}, wantEnd: 4,
		},
		{
			// CR LF of an empty line is still a complete line, and the
			// empty text of it drops the CR.
			name: "EmptyCRLF", head: "\r\n", total: 2, wantLines: []string{""}, wantEnd: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines, end := forkNewLines(tc.head, tc.tail, tc.total, tc.cursor)
			require.Equal(t, tc.wantLines, lines)
			require.Equal(t, tc.wantEnd, end)
		})
	}
}

// The output that the agent reads for a watch is the raw buffer: a head and
// tail that are contiguous are not split by the separator of Output.
func TestForkRaw(t *testing.T) {
	t.Parallel()
	buf := NewHeadTailBufferSized(8, 8)
	_, err := buf.Write([]byte("h1\nERROR part\n"))
	require.NoError(t, err)
	output, truncated := buf.Output()
	require.NotNil(t, truncated)
	require.Zero(t, truncated.OmittedBytes)
	require.Equal(t, "h1\nERROR\n part\n", output, "Output adds a separator")
	head, tail, total := buf.forkRaw()
	require.Equal(t, "h1\nERROR part\n", head+tail)
	require.Equal(t, 14, total)
	lines, end := forkNewLines(head, tail, total, 3)
	require.Equal(t, []string{"ERROR part"}, lines)
	require.Equal(t, 14, end)
}

// A line that ends in CR LF is reported without the trailing CR, so a
// pattern with an end anchor such as ^READY$ matches it.
func TestForkNewLinesCRLF(t *testing.T) {
	t.Parallel()
	lines, end := forkNewLines("READY\r\nb\r\n", "", 10, 0)
	require.Equal(t, []string{"READY", "b"}, lines)
	require.Equal(t, 10, end)
	// A pattern with an end anchor matches the line as reported.
	require.True(t, regexp.MustCompile(`^READY$`).MatchString(lines[0]))
	// Positions still count the raw bytes, so a second poll after the
	// first reports nothing and no line is reported twice.
	lines, end = forkNewLines("READY\r\nb\r\n", "", 10, end)
	require.Empty(t, lines)
	require.Equal(t, 10, end)
	// New bytes after a poll: only the new lines come back.
	lines, end = forkNewLines("READY\r\nb\r\nlast\r\n", "", 16, 10)
	require.Equal(t, []string{"last"}, lines)
	require.Equal(t, 16, end)
	// A line that is still open holds its bytes for the next poll.
	lines, end = forkNewLines("READY\r\nb\r\nopen\r", "", 15, 10)
	require.Empty(t, lines)
	require.Equal(t, 10, end)
}

// The matching path of an output watch reports CR LF lines without the
// trailing CR, so a pattern with an end anchor fires.
func TestForkOutputWatchCRLF(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	api, handler := forkTestAPI(t, mClock)
	client := newForkEventsClient(0)
	stop := forkReport(ctx, t, api, client)
	defer stop()

	gate := filepath.Join(t.TempDir(), "gate")
	chatID, wakeupID := uuid.New(), uuid.New()
	proc := forkStart(t, api, handler, chatID, uuid.New(), fmt.Sprintf(
		"printf 'a\r\nREADY\r\n'; while [ ! -e %q ]; do sleep 0.05; done; sleep 60", gate))
	require.Eventually(t, func() bool {
		_, _, total := proc.buf.forkRaw()
		return total == len("a\r\nREADY\r\n")
	}, testutil.WaitLong, testutil.IntervalFast)

	code, _ := forkWatch(t, handler, chatID, proc.id, workspacesdk.ForkWatchProcessRequest{
		WakeupID: wakeupID, OutputPattern: "^READY$",
	})
	require.Equal(t, http.StatusOK, code)
	mClock.Advance(forkOutputWatchInterval).MustWait(ctx)
	req := testutil.TryReceive(ctx, t, client.calls)
	match := req.GetEvents()[0].GetOutputMatch()
	require.NotNil(t, match)
	require.EqualValues(t, 1, match.GetMatched())
	require.Equal(t, []string{"READY"}, match.GetLines())
	require.Equal(t, wakeupID.String(), match.GetWakeupId())
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
	require.Equal(t, "ERROR \uFFFD!", forkCutLine("ERROR \xff\xfe!"))
}

// Process output that is not valid UTF-8 still makes a report that
// protobuf can encode, so it cannot block the queue.
func TestForkInvalidUTF8(t *testing.T) {
	t.Parallel()
	binary := "ok \xff\xfe\n"
	event := func(tail string, lines ...string) *proto.ReportChatProcessEventsRequest {
		return &proto.ReportChatProcessEventsRequest{Events: []*proto.ReportChatProcessEventsRequest_Event{
			{Kind: &proto.ReportChatProcessEventsRequest_Event_Exit_{Exit: &proto.ReportChatProcessEventsRequest_Event_Exit{OutputTail: tail}}},
			{Kind: &proto.ReportChatProcessEventsRequest_Event_OutputMatch_{OutputMatch: &proto.ReportChatProcessEventsRequest_Event_OutputMatch{Lines: lines}}},
		}}
	}
	_, err := protobuf.Marshal(event(binary, binary))
	require.Error(t, err, "protobuf rejects invalid UTF-8")

	tail := forkOutputTail(binary)
	require.Equal(t, "ok \uFFFD\n", tail)
	long := forkOutputTail(strings.Repeat("\xff", forkMaxOutputTailBytes+10))
	require.True(t, utf8.ValidString(long))
	_, err = protobuf.Marshal(event(tail, forkCutLine(binary), long))
	require.NoError(t, err)
}

// Past the cap of the queue an output match is dropped, the oldest first,
// and an exit is kept without its output.
func TestForkEnqueueCap(t *testing.T) {
	t.Parallel()
	api, _ := forkTestAPI(t, quartz.NewMock(t))
	exit := func(id string) *proto.ReportChatProcessEventsRequest_Event {
		return &proto.ReportChatProcessEventsRequest_Event{
			ProcessId: id,
			Kind:      &proto.ReportChatProcessEventsRequest_Event_Exit_{Exit: &proto.ReportChatProcessEventsRequest_Event_Exit{OutputTail: "bye\n"}},
		}
	}
	output := func(id string) *proto.ReportChatProcessEventsRequest_Event {
		return &proto.ReportChatProcessEventsRequest_Event{
			ProcessId: id,
			Kind:      &proto.ReportChatProcessEventsRequest_Event_OutputMatch_{OutputMatch: &proto.ReportChatProcessEventsRequest_Event_OutputMatch{Matched: 1}},
		}
	}
	ids := func() []string {
		api.fork.mu.Lock()
		defer api.fork.mu.Unlock()
		ids := make([]string, 0, len(api.fork.pending))
		for _, e := range api.fork.pending {
			ids = append(ids, e.GetProcessId())
		}
		return ids
	}

	api.forkEnqueue(exit("exit-0"))
	for i := 1; i < forkMaxPendingProcessEvents; i++ {
		api.forkEnqueue(output(fmt.Sprintf("out-%d", i)))
	}
	require.Len(t, ids(), forkMaxPendingProcessEvents)

	// The oldest output match makes room, not the older exit.
	api.forkEnqueue(exit("exit-1"))
	got := ids()
	require.Len(t, got, forkMaxPendingProcessEvents)
	require.Equal(t, []string{"exit-0", "out-2"}, got[:2])
	require.Equal(t, "exit-1", got[len(got)-1])

	// With only exits queued, an output match is dropped and an exit is
	// kept without its output.
	api.fork.mu.Lock()
	api.fork.pending = api.fork.pending[:0]
	api.fork.mu.Unlock()
	for i := 0; i < forkMaxPendingProcessEvents; i++ {
		api.forkEnqueue(exit(fmt.Sprintf("exit-%d", i)))
	}
	api.forkEnqueue(output("out-late"))
	require.Len(t, ids(), forkMaxPendingProcessEvents)
	api.forkEnqueue(exit("exit-late"))
	got = ids()
	require.Len(t, got, forkMaxPendingProcessEvents+1)
	require.Equal(t, "exit-0", got[0])
	api.fork.mu.Lock()
	last := api.fork.pending[len(api.fork.pending)-1]
	api.fork.mu.Unlock()
	require.Equal(t, "exit-late", last.GetProcessId())
	require.Empty(t, last.GetExit().GetOutputTail())

	// Past forkMaxPendingProcessExits the oldest exit is dropped, so the
	// queue stays bounded.
	for i := len(got); i < forkMaxPendingProcessExits; i++ {
		api.forkEnqueue(exit(fmt.Sprintf("exit-more-%d", i)))
	}
	require.Len(t, ids(), forkMaxPendingProcessExits)
	api.forkEnqueue(exit("exit-over"))
	got = ids()
	require.Len(t, got, forkMaxPendingProcessExits)
	require.Equal(t, "exit-1", got[0])
	require.Equal(t, "exit-over", got[len(got)-1])
}
