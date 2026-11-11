package agentcontext_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/agent/agentcontext"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// fakePusher records every push and lets the test control the
// returned response and error.
type fakePusher struct {
	mu       sync.Mutex
	requests []*agentcontext.PushRequest
	resp     *agentcontext.PushResponse
	err      error
	// errOnce is non-nil to simulate a single transient
	// failure followed by success.
	errOnce error
	signal  chan struct{}
}

func newFakePusher() *fakePusher {
	return &fakePusher{
		resp:   &agentcontext.PushResponse{Accepted: true},
		signal: make(chan struct{}, 16),
	}
}

func (p *fakePusher) PushContextState(_ context.Context, req *agentcontext.PushRequest) (*agentcontext.PushResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if p.errOnce != nil {
		err := p.errOnce
		p.errOnce = nil
		return nil, err
	}
	select {
	case p.signal <- struct{}{}:
	default:
	}
	return p.resp, p.err
}

func (p *fakePusher) snapshot() []*agentcontext.PushRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*agentcontext.PushRequest, len(p.requests))
	copy(out, p.requests)
	return out
}

// waitSettled waits until the pusher has seen at least n settled
// (capability generation zero) pushes and returns them in order. A
// Manager's first connection leads with a capability probe, so settled
// pushes are the ones that carry persisted inventory.
func waitSettled(t *testing.T, p *fakePusher, n int) []*agentcontext.PushRequest {
	t.Helper()
	settled := func() []*agentcontext.PushRequest {
		var out []*agentcontext.PushRequest
		for _, req := range p.snapshot() {
			if req.CapabilityGeneration == 0 {
				out = append(out, req)
			}
		}
		return out
	}
	require.Eventually(t, func() bool {
		return len(settled()) >= n
	}, testutil.WaitShort, testutil.IntervalFast, "expected %d settled pushes", n)
	return settled()
}

func TestRunPush_FirstPushIsInitial(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("v1"), 0o600))

	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return dir },
	})

	p := newFakePusher()
	ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer cancel()

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- m.RunPush(ctx, p, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push"),
		})
	}()

	// A fresh Manager (for example after an agent restart) leads with a
	// capability probe, so a pack-free snapshot never replaces the pack
	// rows a previous agent process stored. The settled successor keeps
	// Initial=true because coderd persisted nothing for the probe.
	settled := waitSettled(t, p, 1)
	requests := p.snapshot()
	require.NotZero(t, requests[0].CapabilityGeneration, "a fresh Manager's first push must be a capability probe")
	require.True(t, requests[0].Initial, "first push must be initial")
	require.True(t, settled[0].Initial, "the probe's settled successor must stay initial")

	cancel()
	require.ErrorIs(t, <-pushDone, context.Canceled)
}

func TestRunPush_SubsequentPushOnChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("v1"), 0o600))

	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return dir },
	})

	p := newFakePusher()
	ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer cancel()

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- m.RunPush(ctx, p, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push"),
		})
	}()

	// Initial settled push.
	waitSettled(t, p, 1)

	// Trigger a resync via Resync.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("v2"), 0o600))
	_, err := m.Resync(ctx)
	require.NoError(t, err)

	// Second settled push.
	requests := waitSettled(t, p, 2)
	require.False(t, requests[1].Initial, "subsequent pushes must not be Initial")
	require.NotEqual(t, requests[0].AggregateHash, requests[1].AggregateHash,
		"second push must reflect the v2 content, not a duplicate of the first snapshot")
	require.Greater(t, requests[1].Version, requests[0].Version,
		"version must advance between snapshots")

	cancel()
	require.ErrorIs(t, <-pushDone, context.Canceled)
}

func TestRunPush_StopsOnUnimplemented(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return t.TempDir() },
	})

	p := newFakePusher()
	p.err = agentcontext.ErrPushUnimplemented

	ctx := testutil.Context(t, testutil.WaitShort)
	err := m.RunPush(ctx, p, agentcontext.PushOptions{
		Logger: testutil.Logger(t).Named("push"),
	})
	require.NoError(t, err, "Unimplemented must stop the loop cleanly")
}

func TestRunPush_RetriesTransientError(t *testing.T) {
	t.Parallel()
	mClock := quartz.NewMock(t)
	trap := mClock.Trap().NewTimer()
	defer trap.Close()

	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return t.TempDir() },
	})

	p := newFakePusher()
	p.errOnce = xerrors.New("transient")

	ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer cancel()
	pushDone := make(chan error, 1)
	go func() {
		pushDone <- m.RunPush(ctx, p, agentcontext.PushOptions{
			Logger:         testutil.Logger(t).Named("push"),
			InitialBackoff: time.Second,
			Clock:          mClock,
		})
	}()

	// First push hits transient and arms the retry timer. Wait for
	// the timer creation, then advance the clock past the backoff.
	call := trap.MustWait(ctx)
	call.MustRelease(ctx)
	mClock.Advance(time.Second).MustWait(ctx)

	select {
	case <-p.signal:
	case <-time.After(testutil.WaitShort):
		t.Fatalf("expected push after transient error")
	}
	require.GreaterOrEqual(t, len(p.snapshot()), 2)

	cancel()
	<-pushDone
}

// TestRunPush_ClosesOnManagerClose verifies that calling
// Manager.Close terminates an in-flight RunPush even when the
// caller's context is still live. Without this guarantee the
// agent shutdown would leak a push goroutine until the
// surrounding ctx expired.
func TestRunPush_ClosesOnManagerClose(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return t.TempDir() },
	})

	p := newFakePusher()
	ctx := testutil.Context(t, testutil.WaitShort)
	done := make(chan error, 1)
	go func() {
		done <- m.RunPush(ctx, p, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push"),
		})
	}()

	// Wait for the initial push so the loop is parked on the
	// change channel, then close the Manager and assert that
	// RunPush returns promptly with a nil error.
	select {
	case <-p.signal:
	case <-ctx.Done():
		t.Fatalf("initial push never landed: %v", ctx.Err())
	}
	require.NoError(t, m.Close())

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatalf("RunPush did not return after Manager.Close: %v", ctx.Err())
	}
}

// TestRunPush_RejectedResponseProceeds verifies the contract
// that an Accepted=false response is not retried: pushWithRetry
// returns success and RunPush parks on the next change instead
// of re-sending the same snapshot. A regression that added
// retry-on-reject logic would loop here and fail the test.
func TestRunPush_RejectedResponseProceeds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("v1"), 0o600))

	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return dir },
	})

	p := newFakePusher()
	p.resp = &agentcontext.PushResponse{Accepted: false}

	ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer cancel()
	pushDone := make(chan error, 1)
	go func() {
		pushDone <- m.RunPush(ctx, p, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push"),
		})
	}()

	// Initial push delivered and accepted=false; loop must park
	// on changes, not retry the same payload.
	waitSettled(t, p, 1)

	// Trigger a content change so a second push lands. Without
	// the change, the loop should remain parked.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("v2"), 0o600))
	_, err := m.Resync(ctx)
	require.NoError(t, err)

	requests := waitSettled(t, p, 2)
	require.GreaterOrEqual(t, len(requests), 2,
		"exactly one push per snapshot; rejection must not double-fire")
	require.NotEqual(t, requests[0].AggregateHash, requests[1].AggregateHash)

	cancel()
	require.ErrorIs(t, <-pushDone, context.Canceled)
}

// TestRunPush_WaitsForReady verifies the push loop ships nothing while
// the Manager is gated and ships the complete inventory once SetReady
// fires, so coderd never sees pre-startup partial state.
func TestRunPush_WaitsForReady(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Content exists from the start, but the Manager is gated: nothing
	// is pushed until SetReady.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("rules"), 0o600))

	m := newPendingTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return dir },
	})

	p := newFakePusher()
	ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitLong))
	defer cancel()

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- m.RunPush(ctx, p, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push"),
		})
	}()

	// While gated, no push must be sent even though AGENTS.md exists.
	select {
	case <-p.signal:
		t.Fatal("push loop shipped a snapshot before SetReady")
	case <-time.After(testutil.IntervalMedium):
	}
	require.Empty(t, p.snapshot(), "no push must happen before the gate releases")

	// Startup completes; the gate releases and the first real snapshot
	// is pushed with Initial=true.
	m.SetReady()

	requests := waitSettled(t, p, 1)
	first := requests[0]
	require.True(t, first.Initial, "first push after the gate releases must be Initial")
	require.NotEmpty(t, first.Resources, "first push must carry the resolved inventory")
	require.Empty(t, first.SnapshotError, "first push must not carry a transient snapshot error")

	cancel()
	require.ErrorIs(t, <-pushDone, context.Canceled)
}

func TestRunPush_AdvertisedPacksTriggerResync(t *testing.T) {
	t.Parallel()

	// The first push runs before the server advertised the fork-plugin-packs
	// capability, so its snapshot carries no pack rows. A response that flips
	// the capability on must trigger an immediate resync whose push includes
	// the pack, instead of waiting for an unrelated filesystem event. A
	// later false response must retract the capability: the next resolve
	// drops the pack rows again.
	dir := t.TempDir()
	writePack(t, dir, "plugin.json", `{"name": "pstack"}`, "skills", "agents")

	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return dir },
	})

	p := newFakePusher()
	p.mu.Lock()
	p.resp = &agentcontext.PushResponse{Accepted: true, ForkPluginPacks: true}
	p.mu.Unlock()

	ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer cancel()

	// The advertise-triggered resync lands on the Run goroutine's select;
	// Trigger is a no-op without it, so run the loop as production does.
	runDone := make(chan error, 1)
	go func() { runDone <- m.Run(ctx) }()

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- m.RunPush(ctx, p, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push"),
		})
	}()

	// Initial push: no advertisement has been seen yet, so the snapshot
	// must not carry pack rows.
	<-p.signal
	var packRows int
	for _, req := range p.snapshot() {
		if req.CapabilityGeneration == 0 {
			continue
		}
		for _, res := range req.Resources {
			if res.SourcePath != "" {
				packRows++
			}
		}
	}
	require.Zero(t, packRows, "pack rows stay hidden before the server advertises")

	// The first push's response advertised; the triggered resync must
	// produce a push whose snapshot includes the pack.
	select {
	case <-p.signal:
	case <-time.After(testutil.WaitShort):
		t.Fatalf("expected a resync push after the capability was advertised")
	}
	require.Eventually(t, func() bool {
		reqs := p.snapshot()
		if len(reqs) < 2 {
			return false
		}
		for _, res := range reqs[len(reqs)-1].Resources {
			if res.SourcePath != "" {
				return true
			}
		}
		return false
	}, testutil.WaitShort, testutil.IntervalFast,
		"the post-advertisement push must include pack rows")

	// Flip the capability off: the next resolve must drop the pack rows.
	p.mu.Lock()
	p.resp = &agentcontext.PushResponse{Accepted: true}
	p.mu.Unlock()
	// Resync explicitly: negotiation settles synchronously, so this point
	// can be reached before Run's watcher has registered its watches.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("v2"), 0o600))
	_, err := m.Resync(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		reqs := p.snapshot()
		if len(reqs) < 3 {
			return false
		}
		for _, res := range reqs[len(reqs)-1].Resources {
			if res.SourcePath != "" {
				return false
			}
		}
		return true
	}, testutil.WaitShort, testutil.IntervalFast,
		"losing the capability must drop pack rows from the next push")

	cancel()
	require.ErrorIs(t, <-pushDone, context.Canceled)
}

func TestRunPush_NewConnectionStartsUnadvertised(t *testing.T) {
	t.Parallel()

	// The capability is per connection: a reconnect to a replica that
	// does not advertise must not lead its initial push with the pack
	// rows the previous connection's snapshot still carries.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "v1")
	writePack(t, dir, "plugin.json", `{"name": "pstack"}`, "skills", "agents")

	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return dir },
	})

	// Run the loop so advertise-triggered resyncs land, as production does.
	runCtx, runCancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer runCancel()
	go func() { _ = m.Run(runCtx) }()

	p1 := newFakePusher()
	p1.mu.Lock()
	p1.resp = &agentcontext.PushResponse{Accepted: true, ForkPluginPacks: true}
	p1.mu.Unlock()

	ctx1, cancel1 := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	done1 := make(chan error, 1)
	go func() {
		done1 <- m.RunPush(ctx1, p1, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push1"),
		})
	}()

	<-p1.signal
	require.Eventually(t, func() bool {
		reqs := p1.snapshot()
		if len(reqs) < 2 {
			return false
		}
		for _, res := range reqs[len(reqs)-1].Resources {
			if res.SourcePath != "" {
				return true
			}
		}
		return false
	}, testutil.WaitShort, testutil.IntervalFast,
		"the advertised connection must push pack rows")

	reqs1 := p1.snapshot()
	require.NotEmpty(t, reqs1)
	require.NotZero(t, reqs1[0].CapabilityGeneration,
		"a fresh Manager's first connection negotiates, so its initial push is a capability probe")
	require.Equal(t, uint64(0), reqs1[len(reqs1)-1].CapabilityGeneration,
		"settling pushes after an advertisement flip are not negotiation pushes")

	cancel1()
	require.ErrorIs(t, <-done1, context.Canceled)

	// Reconnect to a replica that does not advertise: the initial push
	// must be pack-free even though the Manager kept the advertised
	// flag from the previous connection.
	p2 := newFakePusher()
	p2.mu.Lock()
	p2.resp = &agentcontext.PushResponse{Accepted: true}
	p2.mu.Unlock()

	ctx2, cancel2 := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	done2 := make(chan error, 1)
	go func() {
		done2 <- m.RunPush(ctx2, p2, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push2"),
		})
	}()

	<-p2.signal
	require.Eventually(t, func() bool {
		reqs := p2.snapshot()
		if len(reqs) < 1 {
			return false
		}
		for _, res := range reqs[len(reqs)-1].Resources {
			if res.SourcePath != "" {
				return false
			}
		}
		return true
	}, testutil.WaitShort, testutil.IntervalFast,
		"the reconnected initial push must not carry pack rows")

	reqs2 := p2.snapshot()
	require.NotEmpty(t, reqs2)
	first2 := reqs2[0]
	require.NotZero(t, first2.CapabilityGeneration,
		"the reconnect's first push is the negotiation push and must be marked as capability input")
	require.NotEmpty(t, first2.SnapshotError,
		"the negotiation push marks the snapshot as renegotiating so the server treats it as input")

	// No capability flipped on the reconnect (the reset already cleared
	// both atomics and the peer reports them off), so only the settle
	// path can produce the generation-zero successor push.
	require.Eventually(t, func() bool {
		reqs := p2.snapshot()
		return len(reqs) >= 2 && reqs[len(reqs)-1].CapabilityGeneration == 0
	}, testutil.WaitShort, testutil.IntervalFast,
		"the negotiation push must settle with a generation-zero push even without a capability flip")
	settle2 := p2.snapshot()[len(p2.snapshot())-1]
	require.Empty(t, settle2.SnapshotError,
		"the settling push carries a settled snapshot, not renegotiation input")

	cancel2()
	require.ErrorIs(t, <-done2, context.Canceled)
}

func TestRunPush_ResetFailureRetriesNegotiation(t *testing.T) {
	t.Parallel()

	// A reset whose resync fails (for example the connection's context
	// is already canceled) must not leave the manager able to push the
	// retained pack-bearing snapshot as settled inventory: the
	// negotiation window stays open, so every publish inside it is a
	// non-persisting capability probe and the next healthy connection
	// retries the reset.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "v1")
	writePack(t, dir, "plugin.json", `{"name": "pstack"}`, "skills", "agents")

	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return dir },
	})

	runCtx, runCancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer runCancel()
	go func() { _ = m.Run(runCtx) }()

	p1 := newFakePusher()
	p1.mu.Lock()
	p1.resp = &agentcontext.PushResponse{Accepted: true, ForkPluginPacks: true}
	p1.mu.Unlock()

	ctx1, cancel1 := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	done1 := make(chan error, 1)
	go func() {
		done1 <- m.RunPush(ctx1, p1, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push1"),
		})
	}()

	<-p1.signal
	require.Eventually(t, func() bool {
		reqs := p1.snapshot()
		if len(reqs) == 0 {
			return false
		}
		for _, res := range reqs[len(reqs)-1].Resources {
			if res.SourcePath != "" {
				return true
			}
		}
		return false
	}, testutil.WaitShort, testutil.IntervalFast,
		"the advertised connection must push pack rows")

	cancel1()
	require.ErrorIs(t, <-done1, context.Canceled)

	// A connection whose context is already canceled cannot rebuild the
	// snapshot: the reset fails, RunPush reports the error, and nothing
	// is pushed.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	p2 := newFakePusher()
	err2 := m.RunPush(ctx2, p2, agentcontext.PushOptions{
		Logger: testutil.Logger(t).Named("push2"),
	})
	require.ErrorIs(t, err2, context.Canceled)
	require.Empty(t, p2.snapshot(),
		"the failed connection must not push the retained pack-bearing snapshot")

	// Any resolve inside the open window publishes a probe, not
	// settled inventory, so a concurrent change cannot settle chats
	// against the temporary state.
	snap, err := m.Resync(context.Background())
	require.NoError(t, err)
	require.NotZero(t, snap.CapabilityGeneration,
		"publishes inside the negotiation window must stay probes")
	require.Equal(t, "plugin-pack capability renegotiating", snap.SnapshotError)

	// The next healthy connection retries the reset: its first push is
	// a pack-free probe, and after the peer advertises, the settle push
	// carries the pack rows as settled inventory.
	p3 := newFakePusher()
	p3.mu.Lock()
	p3.resp = &agentcontext.PushResponse{Accepted: true, ForkPluginPacks: true}
	p3.mu.Unlock()

	ctx3, cancel3 := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	done3 := make(chan error, 1)
	go func() {
		done3 <- m.RunPush(ctx3, p3, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push3"),
		})
	}()

	<-p3.signal
	require.Eventually(t, func() bool {
		return len(p3.snapshot()) >= 1
	}, testutil.WaitShort, testutil.IntervalFast)
	first3 := p3.snapshot()[0]
	require.NotZero(t, first3.CapabilityGeneration,
		"the retried reset's first push is a capability probe")
	for _, res := range first3.Resources {
		require.Empty(t, res.SourcePath,
			"the probe push must be pack-free")
	}

	require.Eventually(t, func() bool {
		reqs := p3.snapshot()
		if len(reqs) < 2 {
			return false
		}
		last := reqs[len(reqs)-1]
		if last.CapabilityGeneration != 0 {
			return false
		}
		for _, res := range last.Resources {
			if res.SourcePath != "" {
				return true
			}
		}
		return false
	}, testutil.WaitShort, testutil.IntervalFast,
		"after the peer advertises, the settle push must carry pack rows as settled inventory")

	cancel3()
	require.ErrorIs(t, <-done3, context.Canceled)
}

func TestRunPush_SupersededResetFailsConnection(t *testing.T) {
	t.Parallel()

	// A reset whose resync pass is superseded by a newer resolve
	// returns the previously published snapshot (Resync's stale-epoch
	// path) instead of the probe it built, and that snapshot can be the
	// retained pack-bearing inventory. RunPush must fail the connection
	// instead of pushing it, and the still-open window must make the
	// next connection retry the reset.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "v1")
	writePack(t, dir, "plugin.json", `{"name": "pstack"}`, "skills", "agents")

	// Resolver.MCPResources runs exactly once per resolve, after the
	// filesystem walk, so gating it parks a resolve mid-pass. Once
	// armed, the first gated call parks the connection's reset resolve
	// and the second parks a manual resync started after the reset
	// claimed its resolve epoch; later calls pass through.
	var armed atomic.Bool
	var gated atomic.Int32
	gateA := make(chan struct{})
	gateB := make(chan struct{})
	blockedA := make(chan struct{}, 1)
	blockedB := make(chan struct{}, 1)
	resolver := &agentcontext.Resolver{}
	resolver.MCPResources = func() []agentcontext.Resource {
		if !armed.Load() {
			return nil
		}
		switch gated.Add(1) {
		case 1:
			blockedA <- struct{}{}
			<-gateA
		case 2:
			blockedB <- struct{}{}
			<-gateB
		}
		return nil
	}

	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return dir },
		Resolver:   resolver,
	})
	// Manager.Run is deliberately not started: its watcher-driven
	// resolves also call MCPResources and could take a gate slot. The
	// advertisement flip below is followed by a manual Resync instead,
	// and the settle after a probe is synchronous in RunPush.

	p1 := newFakePusher()
	p1.mu.Lock()
	p1.resp = &agentcontext.PushResponse{Accepted: true, ForkPluginPacks: true}
	p1.mu.Unlock()

	ctx1, cancel1 := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	done1 := make(chan error, 1)
	go func() {
		done1 <- m.RunPush(ctx1, p1, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push1"),
		})
	}()

	<-p1.signal
	require.Eventually(t, func() bool {
		if len(p1.snapshot()) == 0 {
			return false
		}
		_, err := m.Resync(ctx1)
		require.NoError(t, err)
		reqs := p1.snapshot()
		for _, res := range reqs[len(reqs)-1].Resources {
			if res.SourcePath != "" {
				return true
			}
		}
		return false
	}, testutil.WaitShort, testutil.IntervalFast,
		"the advertised connection must push pack rows before the gate is armed")

	cancel1()
	require.ErrorIs(t, <-done1, context.Canceled)

	armed.Store(true)

	// The connection's reset resolve parks at the gate after claiming
	// its resolve epoch.
	p2 := newFakePusher()
	ctx2, cancel2 := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer cancel2()
	done2 := make(chan error, 1)
	go func() {
		done2 <- m.RunPush(ctx2, p2, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push2"),
		})
	}()
	select {
	case <-blockedA:
	case <-time.After(testutil.WaitShort):
		t.Fatal("the reset resolve never reached the gate")
	}

	// A manual resync started while the reset resolve is parked claims
	// the newer epoch and parks too, so neither has published.
	manualDone := make(chan agentcontext.Snapshot, 1)
	manualErr := make(chan error, 1)
	go func() {
		snap, err := m.Resync(context.Background())
		manualDone <- snap
		manualErr <- err
	}()
	select {
	case <-blockedB:
	case <-time.After(testutil.WaitShort):
		t.Fatal("the manual resync never reached the gate")
	}

	// Release the reset resolve first: it finishes after the manual
	// resync claimed its epoch, so the stale-epoch path returns the
	// previously published pack-bearing snapshot and RunPush must
	// refuse it.
	close(gateA)
	require.ErrorContains(t, <-done2, "capability reset superseded by a settled snapshot")
	require.Empty(t, p2.snapshot(),
		"the superseded connection must not push the retained pack-bearing snapshot")

	// Release the manual resync; it publishes a probe because the
	// window is still open.
	close(gateB)
	<-manualDone
	require.NoError(t, <-manualErr)

	// The window stays open, so a resolve still publishes a probe and
	// the next connection retries the reset.
	snap, err := m.Resync(context.Background())
	require.NoError(t, err)
	require.NotZero(t, snap.CapabilityGeneration,
		"publishes inside the negotiation window must stay probes")
	require.Equal(t, "plugin-pack capability renegotiating", snap.SnapshotError)

	// The next healthy connection retries the reset: its first push is
	// a pack-free probe, and the settled successor the response's
	// capabilities produce carries the pack rows.
	p3 := newFakePusher()
	p3.mu.Lock()
	p3.resp = &agentcontext.PushResponse{Accepted: true, ForkPluginPacks: true}
	p3.mu.Unlock()

	ctx3, cancel3 := context.WithCancel(testutil.Context(t, testutil.WaitShort))
	defer cancel3()
	done3 := make(chan error, 1)
	go func() {
		done3 <- m.RunPush(ctx3, p3, agentcontext.PushOptions{
			Logger: testutil.Logger(t).Named("push3"),
		})
	}()

	<-p3.signal
	require.Eventually(t, func() bool {
		return len(p3.snapshot()) >= 2
	}, testutil.WaitShort, testutil.IntervalFast,
		"the retried reset must produce the probe and the settled successor")

	reqs3 := p3.snapshot()
	first3 := reqs3[0]
	require.NotZero(t, first3.CapabilityGeneration,
		"the retried reset's first push is a capability probe")
	for _, res := range first3.Resources {
		require.Empty(t, res.SourcePath,
			"the probe push must be pack-free")
	}
	for _, req := range reqs3[1:] {
		require.Zero(t, req.CapabilityGeneration,
			"pushes after the probe are settled inventory")
	}
	last3 := reqs3[len(reqs3)-1]
	hasPackRows := false
	for _, res := range last3.Resources {
		if res.SourcePath != "" {
			hasPackRows = true
		}
	}
	require.True(t, hasPackRows,
		"after the peer advertises, the settle push must carry pack rows as settled inventory")
	require.Empty(t, last3.SnapshotError,
		"the settling push carries a settled snapshot")

	cancel3()
	require.ErrorIs(t, <-done3, context.Canceled)
}

func TestRunPush_NilPusherErrors(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, agentcontext.ManagerOptions{
		WorkingDir: func() string { return t.TempDir() },
	})
	err := m.RunPush(context.Background(), nil, agentcontext.PushOptions{
		Logger: testutil.Logger(t).Named("push"),
	})
	require.Error(t, err)
}
