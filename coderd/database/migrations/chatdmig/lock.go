package chatdmig

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4/database"
	"github.com/lib/pq"
	"golang.org/x/xerrors"
)

// LockPolicy bounds how long one migration waits for a lock. Postgres queues
// every later query on a table behind an ALTER that waits, so a migration that
// waits for a long transaction stalls all traffic on that table. With
// lock_timeout, the migration gives up, the queue drains, and it tries again.
type LockPolicy struct {
	// Timeout is lock_timeout for each attempt. Zero waits without a limit.
	Timeout time.Duration
	// Attempts is the number of tries before the migration fails.
	Attempts int
	// Backoff is the pause before retry n, which starts at 1.
	Backoff func(retry int) time.Duration
}

// DefaultLockPolicy blocks a table for at most 5 seconds at a time and gives
// up after about 3.5 minutes.
var DefaultLockPolicy = LockPolicy{
	Timeout:  5 * time.Second,
	Attempts: 10,
	Backoff: func(retry int) time.Duration {
		return min(time.Second<<(retry-1), 30*time.Second)
	},
}

// SetLocal returns the statement that applies Timeout to the current
// transaction, or "" for no limit.
func (p LockPolicy) SetLocal() string {
	if p.Timeout <= 0 {
		return ""
	}
	return fmt.Sprintf("SET LOCAL lock_timeout = %d;\n", p.Timeout.Milliseconds())
}

// Retry calls fn until it returns an error that is not a lock conflict, or
// until it has made Attempts attempts. onRetry runs before each pause.
func (p LockPolicy) Retry(ctx context.Context, fn func() error, onRetry func(retry int, err error)) error {
	attempts := max(p.Attempts, 1)
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil || !IsLockConflict(err) {
			return err
		}
		if attempt == attempts {
			return xerrors.Errorf("no lock after %d attempts of %s: %w", attempts, p.Timeout, err)
		}
		if onRetry != nil {
			onRetry(attempt, err)
		}
		var wait time.Duration
		if p.Backoff != nil {
			wait = p.Backoff(attempt)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// IsLockConflict reports a lock timeout or a deadlock, the two errors after
// which a migration can run again unchanged.
func IsLockConflict(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "55P03" || pqErr.Code == "40P01"
	}
	// golang-migrate's database.Error does not unwrap. Its driver returns
	// both the value and the pointer.
	var dbErr database.Error
	if errors.As(err, &dbErr) {
		return IsLockConflict(dbErr.OrigErr)
	}
	var dbErrPtr *database.Error
	if errors.As(err, &dbErrPtr) && dbErrPtr != nil {
		return IsLockConflict(dbErrPtr.OrigErr)
	}
	return false
}
