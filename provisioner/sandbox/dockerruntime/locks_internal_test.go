package dockerruntime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/testutil"
)

func TestLocks(t *testing.T) {
	t.Parallel()

	t.Run("SameIDWaits", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		l := locks{held: map[string]*lock{}}
		release, err := l.acquire(ctx, "a")
		require.NoError(t, err)

		got := make(chan func(), 1)
		go func() {
			r, err := l.acquire(ctx, "a")
			if err == nil {
				got <- r
			}
		}()
		require.Eventually(t, func() bool {
			l.mu.Lock()
			defer l.mu.Unlock()
			return l.held["a"].refs == 2
		}, testutil.WaitShort, testutil.IntervalFast)
		select {
		case <-got:
			t.Fatal("a second call on the same container must wait")
		default:
		}
		release()
		second := testutil.TryReceive(ctx, t, got)
		second()
		require.Empty(t, l.held, "released locks must leave the map")
	})

	t.Run("OtherIDDoesNotWait", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		l := locks{held: map[string]*lock{}}
		ra, err := l.acquire(ctx, "a")
		require.NoError(t, err)
		rb, err := l.acquire(ctx, "b")
		require.NoError(t, err)
		rb()
		ra()
		require.Empty(t, l.held)
	})

	t.Run("CanceledWaitGivesUp", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		l := locks{held: map[string]*lock{}}
		release, err := l.acquire(ctx, "a")
		require.NoError(t, err)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = l.acquire(canceled, "a")
		require.ErrorIs(t, err, context.Canceled)
		release()
		require.Empty(t, l.held, "a canceled wait must not keep the lock alive")
	})
}
