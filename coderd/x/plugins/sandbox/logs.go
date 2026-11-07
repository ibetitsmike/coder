package sandbox

import (
	"io"
	"sync"
	"time"

	"github.com/coder/quartz"
)

const logWindow = time.Minute

var logTruncatedMarker = []byte("\n[plugin output truncated: log limit reached]\n")

// logLimiter caps the bytes written to w per minute. Dropped bytes are
// reported as written so the guest does not retry them.
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
		l.used += n
		if written, err := l.w.Write(p[:n]); err != nil {
			return written, err
		}
	}
	if n < int64(len(p)) && !l.truncated {
		l.truncated = true
		if _, err := l.w.Write(logTruncatedMarker); err != nil {
			return int(n), err
		}
	}
	return len(p), nil
}
