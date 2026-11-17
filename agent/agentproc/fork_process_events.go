package agentproc

// This file is fork code: it reports the exits and the matching output of
// the background processes of chats to coderd over the Agent API, for the
// wait_for_events tool of the fork-chat-wakeups experiment. Upstream does
// not have it.

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
	"storj.io/drpc/drpcerr"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/agent/agentchat"
	"github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	// forkMaxPendingProcessEvents caps the events that wait for coderd's
	// ack. Past it the oldest output match is dropped. Exits are kept, one
	// per chat process, but past the cap they carry no output.
	forkMaxPendingProcessEvents = 1000
	// forkMaxProcessEventsBatch caps the events of one report.
	forkMaxProcessEventsBatch   = 100
	forkProcessEventsMinBackoff = time.Second
	forkProcessEventsMaxBackoff = time.Minute
	// forkProcessEventsHeartbeat is how often the agent tells coderd that
	// it holds no unacked event while a chat process runs, which the final
	// poll of a wake-up waits for.
	forkProcessEventsHeartbeat = 30 * time.Second
	// forkMaxOutputTailBytes caps the output that an exit carries. coderd
	// quotes less of it.
	forkMaxOutputTailBytes = 8192
	// forkOutputWatchInterval is how often an output watch reads the
	// output of its process.
	forkOutputWatchInterval = time.Second
	// forkMaxReportedLines and forkMaxReportedLineBytes cap the matching
	// lines that one report carries. The report still counts every match.
	forkMaxReportedLines     = 100
	forkMaxReportedLineBytes = 4096
)

// forkProcessEvents holds the process events that wait for coderd's ack and
// the output watches. Its zero value is ready to use.
type forkProcessEvents struct {
	mu sync.Mutex
	// started holds the chat processes whose exit is reported, so that a
	// repeated start of the same tool call does not report it twice.
	started map[*process]struct{}
	pending []*proto.ReportChatProcessEventsRequest_Event
	notify  chan struct{}
	watches map[*process]*forkOutputWatch
}

// forkOutputWatch reports the output lines of one process that match its
// pattern, for the wake-up wakeupID.
type forkOutputWatch struct {
	wakeupID uuid.UUID
	stop     chan struct{}
}

func (f *forkProcessEvents) notifyChan() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notify == nil {
		f.notify = make(chan struct{}, 1)
	}
	return f.notify
}

// forkEnqueue adds event to the events that wait for coderd's ack.
func (api *API) forkEnqueue(event *proto.ReportChatProcessEventsRequest_Event) {
	f := &api.fork
	f.mu.Lock()
	if len(f.pending) >= forkMaxPendingProcessEvents {
		log := func(msg string, e *proto.ReportChatProcessEventsRequest_Event) {
			api.logger.Warn(context.Background(), msg,
				slog.F("chat_id", e.GetChatId()),
				slog.F("process_id", e.GetProcessId()),
			)
		}
		i := slices.IndexFunc(f.pending, func(e *proto.ReportChatProcessEventsRequest_Event) bool {
			return e.GetOutputMatch() != nil
		})
		switch {
		case i >= 0:
			log("drop unreported chat process output", f.pending[i])
			f.pending = slices.Delete(f.pending, i, i+1)
		case event.GetExit() != nil:
			// An exit is never dropped: the empty report that follows
			// the drain would tell coderd that none waits.
			log("drop output tail of unreported chat process exit", event)
			event.GetExit().OutputTail = ""
		default:
			log("drop unreported chat process output", event)
			f.mu.Unlock()
			return
		}
	}
	f.pending = append(f.pending, event)
	if f.notify == nil {
		f.notify = make(chan struct{}, 1)
	}
	notify := f.notify
	f.mu.Unlock()
	select {
	case notify <- struct{}{}:
	default:
	}
}

// forkProcessStarted reports the exit of proc, a process that
// handleStartProcess started or found for chat chatID. Processes without a
// chat are not reported.
func (api *API) forkProcessStarted(proc *process, chatID uuid.UUID) {
	if chatID == uuid.Nil {
		return
	}
	f := &api.fork
	now := api.manager.clock.Now()
	f.mu.Lock()
	if f.started == nil {
		f.started = make(map[*process]struct{})
	}
	if _, ok := f.started[proc]; ok {
		f.mu.Unlock()
		return
	}
	// The manager forgets these processes, so a repeated start makes a
	// new one.
	for old := range f.started {
		if info := old.info(); info.ExitedAt != nil && now.Sub(time.Unix(*info.ExitedAt, 0)) > exitedProcessReapAge {
			delete(f.started, old)
		}
	}
	f.started[proc] = struct{}{}
	f.mu.Unlock()

	go func() {
		<-proc.done
		info := proc.info()
		output, _ := proc.output()
		code := -1
		if info.ExitCode != nil {
			code = *info.ExitCode
		}
		at := api.manager.clock.Now()
		if info.ExitedAt != nil {
			at = time.Unix(*info.ExitedAt, 0)
		}
		api.forkEnqueue(&proto.ReportChatProcessEventsRequest_Event{
			ChatId:    chatID.String(),
			ProcessId: proc.id,
			Kind: &proto.ReportChatProcessEventsRequest_Event_Exit_{Exit: &proto.ReportChatProcessEventsRequest_Event_Exit{
				ExitCode:   int32(code), //nolint:gosec // Exit codes fit in int32.
				OutputTail: forkOutputTail(output),
			}},
			At: timestamppb.New(at),
		})
	}()
}

// ReportChatProcessEvents sends the process events of chats to coderd until
// ctx ends. An event leaves the queue once coderd acked it, so the events
// that a lost connection left unacked go out on the next one. A failed
// report is retried with backoff. While a chat process runs and the queue is
// empty, it sends a report without events every forkProcessEventsHeartbeat,
// which tells coderd that no exit waits for its ack. It returns only the
// error of ctx.
func (api *API) ReportChatProcessEvents(ctx context.Context, client proto.DRPCForkChatProcessEventsClient) error {
	f := &api.fork
	notify := f.notifyChan()
	heartbeat := api.manager.clock.NewTicker(forkProcessEventsHeartbeat, "agentproc", "fork", "process-events-heartbeat")
	defer heartbeat.Stop()
	var backoff time.Duration
	beat := false
	for {
		f.mu.Lock()
		batch := slices.Clone(f.pending[:min(len(f.pending), forkMaxProcessEventsBatch)])
		// The queue is read right before the send, so a report without
		// events covers every exit queued before it.
		beat = beat && len(batch) == 0 && f.chatProcessRunningLocked()
		f.mu.Unlock()
		if len(batch) == 0 && !beat {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-notify:
			case <-heartbeat.C:
				beat = true
			}
			continue
		}
		beat = false
		_, err := client.ReportChatProcessEvents(ctx, &proto.ReportChatProcessEventsRequest{Events: batch})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			backoff = min(max(backoff*2, forkProcessEventsMinBackoff), forkProcessEventsMaxBackoff)
			level := api.logger.Warn
			if drpcerr.Code(err) == drpcerr.Unimplemented || strings.Contains(err.Error(), "unknown rpc") {
				// coderd predates the RPC: drpcmux answers an RPC it does
				// not know with a protocol error.
				level = api.logger.Debug
			}
			level(ctx, "report chat process events", slog.F("retry_in", backoff), slog.Error(err))
			timer := api.manager.clock.NewTimer(backoff, "agentproc", "fork", "process-events-backoff")
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		backoff = 0
		acked := make(map[*proto.ReportChatProcessEventsRequest_Event]struct{}, len(batch))
		for _, event := range batch {
			acked[event] = struct{}{}
		}
		f.mu.Lock()
		pending := f.pending[:0:0]
		for _, event := range f.pending {
			if _, ok := acked[event]; !ok {
				pending = append(pending, event)
			}
		}
		f.pending = pending
		f.mu.Unlock()
	}
}

// chatProcessRunningLocked reports whether a chat process whose exit is
// reported still runs. f.mu must be held.
func (f *forkProcessEvents) chatProcessRunningLocked() bool {
	for proc := range f.started {
		if proc.info().Running {
			return true
		}
	}
	return false
}

// forkRoutes adds the fork routes of the process API to r.
func (api *API) forkRoutes(r chi.Router) {
	r.Post("/{id}/watch", api.forkHandleWatchProcess)
}

// forkHandleWatchProcess registers the output watch of a
// workspacesdk.ForkWatchProcessRequest and returns the state of the process
// after it. A process that the agent does not know is not an error: chatd
// reports the watch as lost.
func (api *API) forkHandleWatchProcess(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req workspacesdk.ForkWatchProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Request body must be valid JSON.",
			Detail:  err.Error(),
		})
		return
	}
	var pattern *regexp.Regexp
	chat, _ := agentchat.FromContext(ctx)
	if req.OutputPattern != "" {
		if chat.ID == uuid.Nil {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message: "Output watches require a chat.",
			})
			return
		}
		var err error
		pattern, err = regexp.Compile(req.OutputPattern)
		if err != nil {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message: "Invalid output pattern.",
				Detail:  err.Error(),
			})
			return
		}
	}

	id := chi.URLParam(r, "id")
	proc, ok := api.manager.get(chat.ID, id)
	if !ok {
		httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.ForkWatchProcessResponse{})
		return
	}
	if pattern != nil {
		api.forkWatchOutput(proc, chat.ID, req, pattern)
	}

	// The exit goroutine writes all output before it marks the process
	// exited, so the output read after info is final.
	info := proc.info()
	resp := workspacesdk.ForkWatchProcessResponse{
		Known:    true,
		Command:  info.Command,
		Running:  info.Running,
		ExitCode: info.ExitCode,
		ExitedAt: info.ExitedAt,
	}
	if !info.Running {
		output, _ := proc.output()
		resp.OutputTail = forkOutputTail(output)
	}
	httpapi.Write(ctx, rw, http.StatusOK, resp)
}

// forkWatchOutput starts the output watch of req on proc, unless proc
// already has one for the same wake-up. A watch for another wake-up
// replaces the earlier one.
func (api *API) forkWatchOutput(proc *process, chatID uuid.UUID, req workspacesdk.ForkWatchProcessRequest, pattern *regexp.Regexp) {
	f := &api.fork
	f.mu.Lock()
	if f.watches == nil {
		f.watches = make(map[*process]*forkOutputWatch)
	}
	if old, ok := f.watches[proc]; ok {
		if old.wakeupID == req.WakeupID {
			f.mu.Unlock()
			return
		}
		close(old.stop)
	}
	watch := &forkOutputWatch{wakeupID: req.WakeupID, stop: make(chan struct{})}
	f.watches[proc] = watch
	// Create the ticker before the goroutine runs, so a test that
	// advances a mock clock after the request sees it.
	ticker := api.manager.clock.NewTicker(forkOutputWatchInterval, "agentproc", "fork", "output-watch")
	f.mu.Unlock()

	go func() {
		defer ticker.Stop()
		defer func() {
			f.mu.Lock()
			if f.watches[proc] == watch {
				delete(f.watches, proc)
			}
			f.mu.Unlock()
		}()
		api.forkRunOutputWatch(proc, chatID, watch, req, pattern, ticker.C)
	}()
}

// forkRunOutputWatch reads the output of proc on every tick and reports the
// lines that match pattern: the first match at once, later ones at most once
// per cool-down, and at most OutputMaxEvents reports. It ends after the
// last report, when the watch stops, or when the process exits, whose exit
// report covers the lines that were not reported yet.
func (api *API) forkRunOutputWatch(proc *process, chatID uuid.UUID, watch *forkOutputWatch, req workspacesdk.ForkWatchProcessRequest, pattern *regexp.Regexp, tick <-chan time.Time) {
	maxEvents := max(req.OutputMaxEvents, 1)
	cooldown := time.Duration(max(req.OutputCooldownSeconds, 0)) * time.Second
	var (
		cursor   int
		lines    []string
		matched  int
		sent     int
		lastSent time.Time
	)
	for {
		select {
		case <-watch.stop:
			return
		case <-proc.done:
			return
		case <-tick:
		}
		head, tail, total := proc.buf.forkRaw()
		var newLines []string
		newLines, cursor = forkNewLines(head, tail, total, cursor)
		for _, line := range newLines {
			if !pattern.MatchString(line) {
				continue
			}
			matched++
			lines = append(lines, forkCutLine(line))
			if len(lines) > forkMaxReportedLines {
				lines = lines[1:]
			}
		}
		if matched == 0 {
			continue
		}
		now := api.manager.clock.Now()
		if sent > 0 && now.Before(lastSent.Add(cooldown)) {
			continue
		}
		sent++
		var wakeupID string
		if watch.wakeupID != uuid.Nil {
			wakeupID = watch.wakeupID.String()
		}
		api.forkEnqueue(&proto.ReportChatProcessEventsRequest_Event{
			ChatId:    chatID.String(),
			ProcessId: proc.id,
			Kind: &proto.ReportChatProcessEventsRequest_Event_OutputMatch_{OutputMatch: &proto.ReportChatProcessEventsRequest_Event_OutputMatch{
				Lines:    lines,
				Matched:  int32(matched), //nolint:gosec // Bounded by the output size.
				Last:     sent >= maxEvents,
				WakeupId: wakeupID,
			}},
			At: timestamppb.New(now),
		})
		if sent >= maxEvents {
			return
		}
		lastSent = now
		lines = nil
		matched = 0
	}
}

// forkRaw returns copies of the head and the tail that b keeps, and the
// number of bytes written to it. Unlike Output, it adds no separator and
// cuts no line, so a position in them is a position in the whole output.
func (b *HeadTailBuffer) forkRaw() (head, tail string, total int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.head), string(b.tailBytes()), b.totalBytes
}

// forkNewLines returns the complete lines of output that end after position
// cursor, and the position after the last of them, or cursor when there are
// none. A position counts the bytes of the whole output of the process, of
// which head and tail are the first and the last part, and total the size.
// When bytes between them are omitted, only the lines of the tail can be
// new, and the first of them may have lost its start. Lines that the buffer
// no longer keeps are missed.
func forkNewLines(head, tail string, total, cursor int) ([]string, int) {
	output := head + tail
	start := 0
	if len(output) < total {
		output = tail
		newline := strings.IndexByte(output, '\n')
		if newline < 0 {
			return nil, cursor
		}
		start = newline + 1
	}
	// output ends at position total.
	offset := total - len(output)
	var lines []string
	end := cursor
	for start < len(output) {
		newline := strings.IndexByte(output[start:], '\n')
		if newline < 0 {
			// The process may still be writing this line.
			break
		}
		lineEnd := start + newline + 1
		if offset+lineEnd > cursor {
			lines = append(lines, output[start:lineEnd-1])
			end = offset + lineEnd
		}
		start = lineEnd
	}
	return lines, end
}

// forkCutLine returns the first forkMaxReportedLineBytes bytes of line, cut
// at a rune start, with invalid UTF-8 replaced, since a protobuf string must
// be valid UTF-8.
func forkCutLine(line string) string {
	line = strings.ToValidUTF8(line, "\uFFFD")
	if len(line) <= forkMaxReportedLineBytes {
		return line
	}
	end := forkMaxReportedLineBytes
	for end > 0 && !utf8.RuneStart(line[end]) {
		end--
	}
	return line[:end]
}

// forkOutputTail returns the last forkMaxOutputTailBytes bytes of output,
// from a line start when one is in them, else from a rune start, with
// invalid UTF-8 replaced, since a protobuf string must be valid UTF-8.
func forkOutputTail(output string) string {
	if len(output) <= forkMaxOutputTailBytes {
		return strings.ToValidUTF8(output, "\uFFFD")
	}
	start := len(output) - forkMaxOutputTailBytes
	if output[start-1] != '\n' {
		if newline := strings.IndexByte(output[start:], '\n'); newline >= 0 && start+newline+1 < len(output) {
			start += newline + 1
		} else {
			for start < len(output) && !utf8.RuneStart(output[start]) {
				start++
			}
		}
	}
	return strings.ToValidUTF8(output[start:], "\uFFFD")
}
