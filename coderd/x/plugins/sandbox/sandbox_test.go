package sandbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/coder/coder/v2/coderd/x/plugins/plugintest"
	"github.com/coder/coder/v2/coderd/x/plugins/sandbox"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
	"github.com/coder/coder/v2/testutil"
)

var warmOnce sync.Once

func newModule(t *testing.T, limits sandbox.Limits) (*sandbox.Module, *bytes.Buffer) {
	t.Helper()
	// A cold build of the test plugin can take longer than the deadline.
	wasm := plugintest.TestPlugin(t)
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	// Compiling the test plugin takes about 20s with -race. A module that
	// stays open keeps the compiled code in the shared cache so other tests
	// reuse it.
	warmOnce.Do(func() {
		_, err := sandbox.New(ctx, wasm, sandbox.Limits{}, nil)
		require.NoError(t, err)
	})
	var logs bytes.Buffer
	mod, err := sandbox.New(ctx, wasm, limits, &logs)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mod.Close(context.Background()) })
	return mod, &logs
}

// recorder is a HostFunc that records calls and answers them like coderd.
type recorder struct {
	calls []*pluginproto.HostCall
}

func (r *recorder) host(_ context.Context, call *pluginproto.HostCall) *pluginproto.HostResult {
	r.calls = append(r.calls, call)
	switch c := call.Call.(type) {
	case *pluginproto.HostCall_ConfigGet:
		return &pluginproto.HostResult{Result: &pluginproto.HostResult_ConfigValue{ConfigValue: "value-of-" + c.ConfigGet}}
	case *pluginproto.HostCall_ChatSend:
		return &pluginproto.HostResult{Result: &pluginproto.HostResult_ChatSend{ChatSend: &pluginproto.ChatSendResult{ChatId: "chat-1"}}}
	default:
		return &pluginproto.HostResult{}
	}
}

// sentTexts returns the text of every ChatSend call.
func (r *recorder) sentTexts() []string {
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
		ChatId:     "chat-1",
		ExternalId: "ext-1",
		Messages:   []*pluginproto.ChatText{{Id: 1, Text: text}},
	})
	require.NoError(t, err)
	return data
}

func decodeResult(t *testing.T, out []byte) *pluginproto.Result {
	t.Helper()
	var res pluginproto.Result
	require.NoError(t, proto.Unmarshal(out, &res))
	return &res
}

// sendEvent runs OnEvent with text and returns the plugin's error and the
// texts it sent.
func sendEvent(ctx context.Context, t *testing.T, mod *sandbox.Module, text string) (string, []string, error) {
	t.Helper()
	rec := &recorder{}
	out, err := mod.Call(ctx, sandbox.ExportOnEvent, eventInput(t, text), rec.host)
	if err != nil {
		return "", nil, err
	}
	return decodeResult(t, out).GetError(), rec.sentTexts(), nil
}

func TestManifest(t *testing.T) {
	t.Parallel()
	mod, _ := newModule(t, sandbox.Limits{})
	ctx := testutil.Context(t, testutil.WaitLong)

	manifest, err := mod.Manifest(ctx)
	require.NoError(t, err)
	assert.True(t, proto.Equal(&pluginproto.Manifest{
		Name:        "testplugin",
		Version:     "1.2.3",
		Events:      []string{"chat.status"},
		Chats:       []string{"ext"},
		HttpOrigins: []string{"https://example.com"},
		TickSeconds: 30,
		Webhook:     true,
		ConfigKeys:  []string{"greeting"},
	}, manifest), "manifest: %v", manifest)
}

func TestHostCalls(t *testing.T) {
	t.Parallel()
	mod, logs := newModule(t, sandbox.Limits{})
	ctx := testutil.Context(t, testutil.WaitLong)

	rec := &recorder{}
	out, err := mod.Call(ctx, sandbox.ExportOnTick, nil, rec.host)
	require.NoError(t, err)
	require.Empty(t, decodeResult(t, out).GetError())

	require.Len(t, rec.calls, 3)
	assert.Equal(t, "greeting", rec.calls[0].GetConfigGet())
	assert.Equal(t, "ext-1", rec.calls[1].GetChatSend().GetExternalId())
	assert.Equal(t, "value-of-greeting from tick", rec.calls[1].GetChatSend().GetText())
	assert.False(t, rec.calls[1].GetChatSend().GetNewChat())
	assert.Equal(t, "tick sent to chat-1", rec.calls[2].GetLog().GetMessage())
	assert.Equal(t, "tick stdout\n", logs.String())

	pluginErr, texts, err := sendEvent(ctx, t, mod, "echo hello")
	require.NoError(t, err)
	assert.Empty(t, pluginErr)
	assert.Equal(t, []string{"echo: hello"}, texts)

	req, err := proto.Marshal(&pluginproto.HTTPRequest{
		Method:  "POST",
		Url:     "/hook",
		Headers: []*pluginproto.Header{{Name: "X-Test", Value: "yes"}},
		Body:    []byte("payload"),
	})
	require.NoError(t, err)
	out, err = mod.Call(ctx, sandbox.ExportOnHTTP, req, rec.host)
	require.NoError(t, err)
	var res pluginproto.HTTPResponse
	require.NoError(t, proto.Unmarshal(out, &res))
	assert.EqualValues(t, 201, res.GetStatus())
	var body map[string]string
	require.NoError(t, json.Unmarshal(res.GetBody(), &body))
	assert.Equal(t, map[string]string{
		"method":   "POST",
		"url":      "/hook",
		"body":     "payload",
		"x-test":   "yes",
		"greeting": "value-of-greeting",
	}, body)
}

func TestNilHost(t *testing.T) {
	t.Parallel()
	mod, _ := newModule(t, sandbox.Limits{})
	ctx := testutil.Context(t, testutil.WaitLong)

	out, err := mod.Call(ctx, sandbox.ExportOnTick, nil, nil)
	require.NoError(t, err)
	assert.Contains(t, decodeResult(t, out).GetError(), "host calls are not available")
}

func TestHandlerErrors(t *testing.T) {
	t.Parallel()
	mod, _ := newModule(t, sandbox.Limits{})
	ctx := testutil.Context(t, testutil.WaitLong)

	pluginErr, _, err := sendEvent(ctx, t, mod, "error")
	require.NoError(t, err)
	assert.Equal(t, "handler failed", pluginErr)

	pluginErr, _, err = sendEvent(ctx, t, mod, "panic")
	require.NoError(t, err)
	assert.Contains(t, pluginErr, "plugin panic: boom")

	// A recovered panic does not lose module state.
	_, texts, err := sendEvent(ctx, t, mod, "count")
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, texts)

	_, err = mod.Call(ctx, "coder_alloc", nil, nil)
	require.ErrorContains(t, err, "unknown export")
	_, err = mod.Call(ctx, sandbox.ExportOnTick, []byte("x"), nil)
	require.ErrorContains(t, err, "takes no input")
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	mod, _ := newModule(t, sandbox.Limits{CallTimeout: time.Second})
	ctx := testutil.Context(t, testutil.WaitLong)

	for _, want := range []string{"1", "2"} {
		_, texts, err := sendEvent(ctx, t, mod, "count")
		require.NoError(t, err)
		require.Equal(t, []string{want}, texts)
	}

	_, _, err := sendEvent(ctx, t, mod, "loop")
	require.ErrorContains(t, err, "timed out")

	// The busy loop killed the instance, so the next call runs in a fresh
	// instance with its state reset.
	_, texts, err := sendEvent(ctx, t, mod, "count")
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, texts)
}

func TestMemoryLimit(t *testing.T) {
	t.Parallel()
	mod, logs := newModule(t, sandbox.Limits{MemoryPages: 512})
	ctx := testutil.Context(t, testutil.WaitLong)

	_, _, err := sendEvent(ctx, t, mod, "alloc")
	require.Error(t, err)
	assert.Contains(t, logs.String(), "out of memory")

	_, texts, err := sendEvent(ctx, t, mod, "count")
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, texts)
}

func TestIsolation(t *testing.T) {
	t.Parallel()
	mod, _ := newModule(t, sandbox.Limits{})
	ctx := testutil.Context(t, testutil.WaitLong)

	pluginErr, texts, err := sendEvent(ctx, t, mod, "sandbox")
	require.NoError(t, err)
	require.Empty(t, pluginErr)
	require.Len(t, texts, 1)
	var report map[string]string
	require.NoError(t, json.Unmarshal([]byte(texts[0]), &report))

	for _, probe := range []string{"read_file", "read_dir", "dial"} {
		assert.True(t, strings.HasPrefix(report[probe], "error: "), "%s succeeded: %q", probe, report[probe])
	}
	assert.Empty(t, report["env"])
	assert.Empty(t, report["getenv"])
	assert.Empty(t, report["args"])
}

func TestSizeLimits(t *testing.T) {
	t.Parallel()
	mod, _ := newModule(t, sandbox.Limits{MaxInputBytes: 1024, MaxOutputBytes: 64})
	ctx := testutil.Context(t, testutil.WaitLong)

	_, err := mod.Call(ctx, sandbox.ExportOnEvent, eventInput(t, strings.Repeat("x", 2048)), nil)
	require.ErrorContains(t, err, "1024 byte limit")

	req, err := proto.Marshal(&pluginproto.HTTPRequest{Method: "GET", Url: "/" + strings.Repeat("x", 128)})
	require.NoError(t, err)
	_, err = mod.Call(ctx, sandbox.ExportOnHTTP, req, nil)
	require.ErrorContains(t, err, "64 byte limit")

	// Rejected input or output does not break the instance.
	_, texts, err := sendEvent(ctx, t, mod, "count")
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, texts)
}

func TestLogLimit(t *testing.T) {
	t.Parallel()
	mod, logs := newModule(t, sandbox.Limits{MaxLogBytes: 1000})
	ctx := testutil.Context(t, testutil.WaitLong)

	for range 2 {
		pluginErr, _, err := sendEvent(ctx, t, mod, "log")
		require.NoError(t, err)
		require.Empty(t, pluginErr)
	}
	assert.Equal(t, 1000, strings.Count(logs.String(), "x"))
	assert.Equal(t, 1, strings.Count(logs.String(), "log limit reached"))
}

func TestInvalidModule(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)

	_, err := sandbox.New(ctx, []byte("not wasm"), sandbox.Limits{}, nil)
	require.ErrorContains(t, err, "compile module")

	emptyModule := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	_, err = sandbox.New(ctx, emptyModule, sandbox.Limits{}, nil)
	require.ErrorContains(t, err, "module does not export")
}
