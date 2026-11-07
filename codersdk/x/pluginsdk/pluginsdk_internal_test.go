package pluginsdk

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"
	"google.golang.org/protobuf/proto"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
)

// fakeHost answers host calls with fetch and records them.
type fakeHost struct {
	calls []*pluginproto.HostCall
	fetch func(*pluginproto.HTTPRequest) *pluginproto.HTTPResponse
}

func (f *fakeHost) send(call *pluginproto.HostCall) (*pluginproto.HostResult, error) {
	return f.answer(call), nil
}

func (f *fakeHost) answer(call *pluginproto.HostCall) *pluginproto.HostResult {
	f.calls = append(f.calls, call)
	switch c := call.Call.(type) {
	case *pluginproto.HostCall_ConfigGet:
		return &pluginproto.HostResult{Result: &pluginproto.HostResult_ConfigValue{ConfigValue: "v-" + c.ConfigGet}}
	case *pluginproto.HostCall_ChatSend:
		if c.ChatSend.GetText() == "fail" {
			return &pluginproto.HostResult{Error: "chat send failed"}
		}
		return &pluginproto.HostResult{Result: &pluginproto.HostResult_ChatSend{ChatSend: &pluginproto.ChatSendResult{ChatId: "chat-1"}}}
	case *pluginproto.HostCall_LinkComplete:
		return &pluginproto.HostResult{Result: &pluginproto.HostResult_LinkComplete{LinkComplete: &pluginproto.LinkCompleteResult{Username: "alice"}}}
	case *pluginproto.HostCall_HttpFetch:
		return &pluginproto.HostResult{Result: &pluginproto.HostResult_HttpFetch{HttpFetch: f.fetch(c.HttpFetch)}}
	default:
		return &pluginproto.HostResult{}
	}
}

func decodeResult(t *testing.T, out []byte) string {
	t.Helper()
	var res pluginproto.Result
	require.NoError(t, proto.Unmarshal(out, &res))
	return res.GetError()
}

func TestHandleManifest(t *testing.T) {
	t.Parallel()
	out := handleManifest(&Plugin{Manifest: Manifest{
		Name:        "p",
		Version:     "1",
		Events:      []string{"e"},
		Chats:       []string{"c"},
		HTTPOrigins: []string{"https://o"},
		ConfigKeys:  []string{"k"},
		TickSeconds: 5,
		Webhook:     true,
	}})
	var m pluginproto.Manifest
	require.NoError(t, proto.Unmarshal(out, &m))
	assert.True(t, proto.Equal(&pluginproto.Manifest{
		Name: "p", Version: "1", Events: []string{"e"}, Chats: []string{"c"},
		HttpOrigins: []string{"https://o"}, ConfigKeys: []string{"k"}, TickSeconds: 5, Webhook: true,
	}, &m))

	require.NoError(t, proto.Unmarshal(handleManifest(nil), &m))
	assert.Empty(t, m.GetName())
}

func TestHandleEvent(t *testing.T) {
	t.Parallel()
	input, err := proto.Marshal(&pluginproto.ChatEvent{
		ChatId: "chat-1", ExternalId: "ext", Status: "waiting", Title: "t",
		Messages: []*pluginproto.ChatText{{Id: 7, Text: "hi"}},
	})
	require.NoError(t, err)

	var got ChatEvent
	fake := &fakeHost{}
	p := &Plugin{OnEvent: func(h Host, ev ChatEvent) error {
		got = ev
		chatID, err := h.ChatSendNew(ev.ExternalID, "reply")
		if err != nil {
			return err
		}
		username, err := h.LinkComplete(ev.ExternalID, "code")
		if err != nil {
			return err
		}
		h.Logf("%s %s %s", chatID, username, h.Config("k"))
		_, err = h.ChatSend(ev.ExternalID, "fail")
		return err
	}}
	assert.Equal(t, "chat send failed", decodeResult(t, handleEvent(p, fake.send, input)))
	assert.Equal(t, ChatEvent{
		ChatID: "chat-1", ExternalID: "ext", Status: "waiting", Title: "t",
		Messages: []ChatText{{ID: 7, Text: "hi"}},
	}, got)
	require.Len(t, fake.calls, 5)
	assert.True(t, fake.calls[0].GetChatSend().GetNewChat())
	assert.Equal(t, "code", fake.calls[1].GetLinkComplete().GetCode())
	assert.Equal(t, "k", fake.calls[2].GetConfigGet())
	assert.Equal(t, "chat-1 alice v-k", fake.calls[3].GetLog().GetMessage())
	assert.False(t, fake.calls[4].GetChatSend().GetNewChat())

	assert.Empty(t, decodeResult(t, handleEvent(&Plugin{}, fake.send, input)))
	assert.Contains(t, decodeResult(t, handleEvent(&Plugin{}, fake.send, []byte{0xff})), "decode chat event")
}

func TestHandlePanics(t *testing.T) {
	t.Parallel()
	fake := &fakeHost{}
	p := &Plugin{
		OnTick:  func(Host) error { panic("tick boom") },
		OnEvent: func(Host, ChatEvent) error { return xerrors.New("") },
		OnHTTP:  func(Host, HTTPRequest) HTTPResponse { panic("http boom") },
	}
	assert.Contains(t, decodeResult(t, handleTick(p, fake.send)), "plugin panic: tick boom")
	assert.NotEmpty(t, decodeResult(t, handleEvent(p, fake.send, nil)))

	var res pluginproto.HTTPResponse
	require.NoError(t, proto.Unmarshal(handleHTTP(p, fake.send, nil), &res))
	assert.EqualValues(t, http.StatusInternalServerError, res.GetStatus())
	assert.Contains(t, string(res.GetBody()), "http boom")

	require.NoError(t, proto.Unmarshal(handleHTTP(&Plugin{}, fake.send, nil), &res))
	assert.EqualValues(t, http.StatusNotFound, res.GetStatus())
}

func TestHandleHTTP(t *testing.T) {
	t.Parallel()
	input, err := proto.Marshal(&pluginproto.HTTPRequest{
		Method:  "POST",
		Url:     "/hook?x=1",
		Headers: []*pluginproto.Header{{Name: "X-A", Value: "1"}, {Name: "X-A", Value: "2"}},
		Body:    []byte("body"),
	})
	require.NoError(t, err)
	p := &Plugin{OnHTTP: func(_ Host, req HTTPRequest) HTTPResponse {
		assert.Equal(t, "POST", req.Method)
		assert.Equal(t, "/hook?x=1", req.URL)
		assert.Equal(t, []string{"1", "2"}, req.Header.Values("X-A"))
		assert.Equal(t, []byte("body"), req.Body)
		return HTTPResponse{Header: http.Header{"X-B": {"b"}}, Body: []byte("ok")}
	}}
	var res pluginproto.HTTPResponse
	require.NoError(t, proto.Unmarshal(handleHTTP(p, (&fakeHost{}).send, input), &res))
	assert.EqualValues(t, http.StatusOK, res.GetStatus())
	assert.Equal(t, []byte("ok"), res.GetBody())
	assert.Equal(t, http.Header{"X-B": {"b"}}, headersFromProto(res.GetHeaders()))
}

func TestJSONHelpers(t *testing.T) {
	t.Parallel()
	fake := &fakeHost{fetch: func(req *pluginproto.HTTPRequest) *pluginproto.HTTPResponse {
		switch req.GetUrl() {
		case "https://o/get":
			return &pluginproto.HTTPResponse{Status: 200, Body: []byte(`{"n":1}`)}
		case "https://o/post":
			return &pluginproto.HTTPResponse{Status: 201, Body: req.GetBody()}
		default:
			return &pluginproto.HTTPResponse{Status: 500, Body: []byte("broken")}
		}
	}}
	h := host{send: fake.send}

	var got struct{ N int }
	require.NoError(t, h.GetJSON("https://o/get", &got))
	assert.Equal(t, 1, got.N)
	assert.Equal(t, "GET", fake.calls[0].GetHttpFetch().GetMethod())

	require.NoError(t, h.PostJSON("https://o/post", map[string]int{"n": 2}, &got))
	assert.Equal(t, 2, got.N)
	post := fake.calls[1].GetHttpFetch()
	assert.Equal(t, "POST", post.GetMethod())
	assert.Equal(t, "application/json", headersFromProto(post.GetHeaders()).Get("Content-Type"))

	err := h.GetJSON("https://o/fail", &got)
	require.ErrorContains(t, err, "unexpected status 500: broken")
}
