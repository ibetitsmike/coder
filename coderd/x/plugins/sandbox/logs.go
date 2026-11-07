package sandbox

import (
	"io"
	"sync"
	"time"

	"github.com/coder/quartz"
)

const logWindow = time.Minute

var logTruncatedMarker = []byte("\n[plugin output truncated: log limit reached]\n")

// logLimiter caps the bytes written to w per minute. It always reports full
// writes so the guest never sees an error from its stdout or stderr.
type logLimiter struct {
	mu          sync.Mutex
	w           io.Writer
	limit       int64
	clock       quartz.Clock
	windowStart time.Time
	used        int64
	truncated   bool
}

func newLogLimiter(w io.Writer, limit int64, clock quartz.Clock) *logLimiter {
	return &logLimiter{w: w, limit: limit, clock: clock, windowStart: clock.Now()}
}

func (l *logLimiter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if now.Sub(l.windowStart) >= logWindow {
		l.windowStart = now
		l.used = 0
		l.truncated = false
	}
	remaining := l.limit - l.used
	n := int64(len(p))
	if n > remaining {
		n = max(remaining, 0)
	}
	if n > 0 {
		_, _ = l.w.Write(p[:n])
		l.used += n
	}
	if n < int64(len(p)) && !l.truncated {
		l.truncated = true
		_, _ = l.w.Write(logTruncatedMarker)
	}
	return len(p), nil
}
