package pluginhost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost"
	"github.com/coder/coder/v2/coderd/x/plugins/plugintest"
	"github.com/coder/coder/v2/coderd/x/plugins/sandbox"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
	"github.com/coder/coder/v2/testutil"
)

// recorder is a HostFunc that records calls and answers them like coderd.
type recorder struct {
	mu    sync.Mutex
	calls []*pluginproto.HostCall
	// onCall, if set, runs for every call.
	onCall func(*pluginproto.HostCall)
}

func (r *recorder) host(_ context.Context, call *pluginproto.HostCall) *pluginproto.HostResult {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	onCall := r.onCall
	r.mu.Unlock()
	if onCall != nil {
		onCall(call)
	}
	switch c := call.Call.(type) {
	case *pluginproto.HostCall_ConfigGet:
		return &pluginproto.HostResult{Result: &pluginproto.HostResult_ConfigValue{ConfigValue: "value-of-" + c.ConfigGet}}
	case *pluginproto.HostCall_ChatSend:
		return &pluginproto.HostResult{Result: &pluginproto.HostResult_ChatSend{ChatSend: &pluginproto.ChatSendResult{ChatId: "chat-1"}}}
	default:
		return &pluginproto.HostResult{}
	}
}

func (r *recorder) sentTexts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var texts []string
	for _, call := range r.calls {
		if send := call.GetChatSend(); send != nil {
			texts = append(texts, send.GetText())
		}
	}
	return texts
}

func eventInput(t *testing.T, text string) []byte {
	t.Helper()
	data, err := proto.Marshal(&pluginproto.ChatEvent{
		ExternalId: "ext-1",
		Messages:   []*pluginproto.ChatText{{Id: 1, Text: text}},
	})
	require.NoError(t, err)
	return data
}

// sendEvent runs OnEvent with text and returns the texts the plugin sent.
func sendEvent(ctx context.Context, t *testing.T, c *pluginhost.Client, text string) ([]string, error) {
	t.Helper()
	rec := &recorder{}
	out, err := c.Invoke(ctx, sandbox.ExportOnEvent, eventInput(t, text), rec.host)
	if err != nil {
		return nil, err
	}
	var res pluginproto.Result
	require.NoError(t, proto.Unmarshal(out, &res))
	require.Empty(t, res.GetError())
	return rec.sentTexts(), nil
}

func startInProcess(t *testing.T) *pluginhost.Client {
	t.Helper()
	// The context bounds the lifetime of the plugin host.
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	c, err := pluginhost.StartInProcess(ctx, testutil.Logger(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestInProcess(t *testing.T) {
	t.Parallel()
	wasm := plugintest.TestPlugin(t)
	c := startInProcess(t)
	ctx := testutil.Context(t, testutil.WaitSuperLong)

	_, err := c.Invoke(ctx, sandbox.ExportOnTick, nil, nil)
	require.ErrorContains(t, err, "no module is loaded")

	manifest, err := c.Load(ctx, wasm, sandbox.Limits{CallTimeout: 5 * time.Second})
	require.NoError(t, err)
	assert.Equal(t, "testplugin", manifest.GetName())
	assert.Equal(t, []string{"greeting"}, manifest.GetConfigKeys())

	rec := &recorder{}
	out, err := c.Invoke(ctx, sandbox.ExportOnTick, nil, rec.host)
	require.NoError(t, err)
	var res pluginproto.Result
	require.NoError(t, proto.Unmarshal(out, &res))
	require.Empty(t, res.GetError())
	require.Len(t, rec.calls, 3)
	assert.Equal(t, "greeting", rec.calls[0].GetConfigGet())
	assert.Equal(t, "value-of-greeting from tick", rec.calls[1].GetChatSend().GetText())
	assert.Equal(t, "tick sent to chat-1", rec.calls[2].GetLog().GetMessage())

	texts, err := sendEvent(ctx, t, c, "echo hi")
	require.NoError(t, err)
	assert.Equal(t, []string{"echo: hi"}, texts)

	// A nil host rejects host calls.
	out, err = c.Invoke(ctx, sandbox.ExportOnTick, nil, nil)
	require.NoError(t, err)
	require.NoError(t, proto.Unmarshal(out, &res))
	assert.Contains(t, res.GetError(), "host calls are not available")

	_, err = sendEvent(ctx, t, c, "loop")
	require.ErrorContains(t, err, "timed out")
	texts, err = sendEvent(ctx, t, c, "count")
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, texts)

	// Canceling an invocation returns at once and keeps the connection. The
	// next call waits for the sandbox to time out the spinning guest.
	cancelCtx, cancel := context.WithCancel(ctx)
	spin := &recorder{onCall: func(*pluginproto.HostCall) { cancel() }}
	_, err = c.Invoke(cancelCtx, sandbox.ExportOnEvent, eventInput(t, "spin"), spin.host)
	require.ErrorIs(t, err, context.Canceled)
	texts, err = sendEvent(ctx, t, c, "count")
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, texts)

	require.NoError(t, c.Close())
	_, err = c.Invoke(ctx, sandbox.ExportOnTick, nil, nil)
	require.ErrorContains(t, err, "plugin host exited")
}

// safeBuffer is a bytes.Buffer safe for concurrent use.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startProcess(ctx context.Context, t *testing.T) (*pluginhost.Client, *safeBuffer) {
	t.Helper()
	var stderr safeBuffer
	c, err := pluginhost.StartProcess(ctx, testutil.Logger(t), pluginhost.ProcessOptions{
		Executable: os.Args[0],
		Stderr:     &stderr,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c, &stderr
}

func childEnv(t *testing.T, stderr string) []string {
	t.Helper()
	for line := range strings.Lines(stderr) {
		if rest, ok := strings.CutPrefix(line, "CHILD_ENV="); ok {
			var env []string
			require.NoError(t, json.Unmarshal([]byte(rest), &env))
			return env
		}
	}
	require.FailNow(t, "child did not report its environment", "stderr: %s", stderr)
	return nil
}

func TestProcess(t *testing.T) {
	t.Parallel()
	wasm := plugintest.TestPlugin(t)
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	require.Equal(t, "secret", os.Getenv(canaryEnv))

	start := time.Now()
	c, stderr := startProcess(ctx, t)
	manifest, err := c.Load(ctx, wasm, sandbox.Limits{})
	require.NoError(t, err)
	t.Logf("start and load took %s", time.Since(start))
	assert.Equal(t, "testplugin", manifest.GetName())

	texts, err := sendEvent(ctx, t, c, "echo from child")
	require.NoError(t, err)
	assert.Equal(t, []string{"echo: from child"}, texts)

	// Close kills the child even while the guest is busy.
	spinning := make(chan struct{})
	spin := &recorder{onCall: func(*pluginproto.HostCall) { close(spinning) }}
	invokeErr := make(chan error, 1)
	go func() {
		_, err := c.Invoke(ctx, sandbox.ExportOnEvent, eventInput(t, "spin"), spin.host)
		invokeErr <- err
	}()
	testutil.TryReceive(ctx, t, spinning)
	require.NoError(t, c.Close())
	require.Error(t, testutil.TryReceive(ctx, t, invokeErr))
	testutil.TryReceive(ctx, t, c.Done())
	require.Error(t, c.Wait(), "the child was killed")

	// Wait returns after the child's stderr is fully copied.
	env := childEnv(t, stderr.String())
	assert.Contains(t, env, "GOMAXPROCS=1")
	// Libraries linked into the binary may set variables of their own at
	// init, but no variable of the parent may reach the child.
	parent := os.Environ()
	for _, kv := range env {
		assert.NotContains(t, kv, canaryEnv)
		if kv != "GOMAXPROCS=1" {
			assert.NotContains(t, parent, kv, "the child inherited %s", kv)
		}
	}
}

func TestProcessContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitLong))
	c, _ := startProcess(ctx, t)
	cancel()
	testutil.TryReceive(testutil.Context(t, testutil.WaitLong), t, c.Done())
	require.Error(t, c.Wait())
}
