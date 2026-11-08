package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
)

const (
	testAPIURL = "http://signal.test"
	alice      = "+15550000001"
	bob        = "+15550000002"
)

// qrCodePNG is what the fake server returns for a link QR code.
var qrCodePNG = []byte("\x89PNG\r\n\x1a\nqr")

// fakeSignal is a signal-cli-rest-api server with one queue of messages for
// each account.
type fakeSignal struct {
	t        *testing.T
	accounts []string
	queues   map[string][]string
	failing  map[string]bool
	polled   []string
	sent     []sendRequest
	// linkError makes the QR code endpoint fail.
	linkError bool
}

func newFakeSignal(t *testing.T, accounts ...string) *fakeSignal {
	return &fakeSignal{t: t, accounts: accounts, queues: map[string][]string{}, failing: map[string]bool{}}
}

func (f *fakeSignal) queue(id string, envelopes ...string) {
	f.queues[id] = append(f.queues[id], envelopes...)
}

func (f *fakeSignal) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/accounts":
		_ = json.NewEncoder(rw).Encode(f.accounts)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/receive/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/receive/")
		f.polled = append(f.polled, id)
		assert.Equal(f.t, "true", r.URL.Query().Get("ignore_attachments"))
		if f.failing[id] {
			http.Error(rw, `{"error":"device was removed"}`, http.StatusBadRequest)
			return
		}
		items := make([]json.RawMessage, 0, len(f.queues[id]))
		for _, e := range f.queues[id] {
			items = append(items, json.RawMessage(`{"account":"`+id+`","envelope":`+e+`}`))
		}
		delete(f.queues, id)
		_ = json.NewEncoder(rw).Encode(items)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/qrcodelink":
		assert.Equal(f.t, deviceName, r.URL.Query().Get("device_name"))
		if f.linkError {
			http.Error(rw, `{"error":"signal-cli exited"}`, http.StatusBadRequest)
			return
		}
		rw.Header().Set("Content-Type", "image/png")
		_, _ = rw.Write(qrCodePNG)
	case r.Method == http.MethodPost && r.URL.Path == "/v2/send":
		var req sendRequest
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&req))
		f.sent = append(f.sent, req)
		rw.WriteHeader(http.StatusCreated)
		_, _ = rw.Write([]byte(`{"timestamp":"1"}`))
	default:
		http.NotFound(rw, r)
	}
}

func (f *fakeSignal) sentTexts() []string {
	out := make([]string, 0, len(f.sent))
	for _, s := range f.sent {
		out = append(out, fmt.Sprintf("%s>%s: %s", s.Number, strings.Join(s.Recipients, ","), s.Message))
	}
	return out
}

type chatCall struct {
	externalID, text string
	newChat          bool
}

// fakeHost is the coderd side of the plugin. Its Fetch serves requests with
// the fake signal-cli-rest-api server.
type fakeHost struct {
	signal  http.Handler
	codes   map[string]string
	linked  map[string]string
	chats   []chatCall
	chatErr error
	logs    []string
}

var _ pluginsdk.Host = (*fakeHost)(nil)

func (*fakeHost) Config(string) string { return "" }

func (h *fakeHost) ChatSend(externalID, text string) (string, error) {
	return h.chat(externalID, text, false)
}

func (h *fakeHost) ChatSendNew(externalID, text string) (string, error) {
	return h.chat(externalID, text, true)
}

func (h *fakeHost) chat(externalID, text string, newChat bool) (string, error) {
	if h.chatErr != nil {
		return "", h.chatErr
	}
	h.chats = append(h.chats, chatCall{externalID: externalID, text: text, newChat: newChat})
	return "chat-1", nil
}

func (h *fakeHost) LinkComplete(externalID, code string) (string, error) {
	username, ok := h.codes[code]
	if !ok {
		return "", xerrors.New("invalid or expired link code")
	}
	delete(h.codes, code)
	if h.linked == nil {
		h.linked = map[string]string{}
	}
	h.linked[externalID] = username
	return username, nil
}

func (h *fakeHost) Fetch(req pluginsdk.HTTPRequest) (pluginsdk.HTTPResponse, error) {
	if !strings.HasPrefix(req.URL, testAPIURL+"/") {
		return pluginsdk.HTTPResponse{}, xerrors.Errorf("origin of %s is not granted", req.URL)
	}
	r := httptest.NewRequest(req.Method, req.URL, bytes.NewReader(req.Body))
	rec := httptest.NewRecorder()
	h.signal.ServeHTTP(rec, r)
	return pluginsdk.HTTPResponse{StatusCode: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}, nil
}

func (h *fakeHost) GetJSON(url string, v any) error {
	return h.doJSON(pluginsdk.HTTPRequest{Method: http.MethodGet, URL: url}, v)
}

func (h *fakeHost) PostJSON(url string, body, v any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return h.doJSON(pluginsdk.HTTPRequest{Method: http.MethodPost, URL: url, Body: data}, v)
}

func (h *fakeHost) doJSON(req pluginsdk.HTTPRequest, v any) error {
	res, err := h.Fetch(req)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return xerrors.Errorf("%s %s: unexpected status %d: %s", req.Method, req.URL, res.StatusCode, bytes.TrimSpace(res.Body))
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(res.Body, v)
}

func (h *fakeHost) Logf(format string, args ...any) {
	h.logs = append(h.logs, fmt.Sprintf(format, args...))
}

func newTest(t *testing.T, accounts ...string) (*bridge, *fakeHost, *fakeSignal) {
	t.Helper()
	signal := newFakeSignal(t, accounts...)
	h := &fakeHost{signal: signal, codes: map[string]string{}}
	return newBridge(testAPIURL + "/"), h, signal
}

// directMessage is an envelope that a contact sent to the account.
func directMessage(from, text string) string {
	return fmt.Sprintf(`{"sourceNumber":%q,"sourceDevice":1,"dataMessage":{"message":%q}}`, from, text)
}

// noteToSelf is an envelope that another device of the account sent to the
// account's own Note to Self chat.
func noteToSelf(account, text string) string {
	return sentTo(account, account, text)
}

func sentTo(account, destination, text string) string {
	return fmt.Sprintf(`{"sourceNumber":%q,"sourceDevice":1,"syncMessage":{"sentMessage":`+
		`{"destination":%q,"destinationNumber":%q,"message":%q}}}`, account, destination, destination, text)
}

func TestNoteToSelf(t *testing.T) {
	t.Parallel()

	b, h, signal := newTest(t, alice)
	h.codes["K7Q-2MX"] = "dana"
	signal.queue(alice,
		noteToSelf(alice, "/link K7Q-2MX"),
		// The private conversations of the account never reach Coder.
		directMessage(bob, "dinner at 8?"),
		sentTo(alice, bob, "sounds good"),
		`{"sourceNumber":"`+alice+`","syncMessage":{"sentMessage":{"destinationNumber":"`+alice+
			`","message":"group note","groupInfo":{"groupId":"g1"}}}}`,
		// A reply of the plugin that another deployment received.
		noteToSelf(alice, replyMarker+"[Other chat] Done."),
		noteToSelf(alice, "why does TestChatWatch flake?"),
	)

	require.NoError(t, b.onTick(h))
	assert.Equal(t, map[string]string{alice: "dana"}, h.linked)
	assert.Equal(t, []chatCall{{externalID: alice, text: "why does TestChatWatch flake?"}}, h.chats)
	assert.Equal(t, []string{alice + ">" + alice + ": " + replyMarker + "Linked to Coder user dana. " + helpText}, signal.sentTexts())

	require.NoError(t, b.onEvent(h, pluginsdk.ChatEvent{
		ExternalID: alice, Title: "TestChatWatch", Messages: []pluginsdk.ChatText{{ID: 1, Text: "The test reads too early."}},
	}))
	require.NoError(t, b.onEvent(h, pluginsdk.ChatEvent{ExternalID: alice, Title: "TestChatWatch", Status: "error"}))
	assert.Equal(t, []string{
		alice + ">" + alice + ": " + replyMarker + "[TestChatWatch] The test reads too early.",
		alice + ">" + alice + ": " + replyMarker + "[TestChatWatch] The chat stopped with an error. Open it in Coder for details.",
	}, signal.sentTexts()[1:])
}

func TestAccountWithoutNumber(t *testing.T) {
	t.Parallel()

	const aci = "11111111-1111-4111-8111-111111111111"
	b, h, signal := newTest(t)
	h.signal = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/accounts" {
			_, _ = rw.Write([]byte(`[{"number":"","aci":"` + aci + `"}]`))
			return
		}
		signal.ServeHTTP(rw, r)
	})
	signal.queue(aci, `{"sourceUuid":"`+aci+`","syncMessage":{"sentMessage":{"destinationUuid":"`+aci+`","message":"hello"}}}`)

	require.NoError(t, b.onTick(h))
	assert.Equal(t, []chatCall{{externalID: aci, text: "hello"}}, h.chats)
}

func TestNewDeviceGetsWelcome(t *testing.T) {
	t.Parallel()

	b, h, signal := newTest(t, alice)
	require.NoError(t, b.onTick(h))
	assert.Empty(t, signal.sent, "devices that existed before the plugin started get no welcome")

	signal.accounts = append(signal.accounts, bob)
	require.NoError(t, b.onTick(h))
	require.NoError(t, b.onTick(h))
	assert.Equal(t, []string{bob + ">" + bob + ": " + replyMarker + welcomeText}, signal.sentTexts())
}

func TestDevicePollingRotates(t *testing.T) {
	t.Parallel()

	devices := []string{"+15550000011", "+15550000012", "+15550000013", "+15550000014", "+15550000015"}
	b, h, signal := newTest(t, devices...)
	signal.failing[devices[0]] = true

	require.NoError(t, b.onTick(h), "a failing device must not fail the tick")
	require.NoError(t, b.onTick(h))
	assert.Equal(t, []string{
		devices[0], devices[1], devices[2], devices[3],
		devices[4], devices[0], devices[1], devices[2],
	}, signal.polled)
	require.Len(t, h.logs, 1, "a device that keeps failing logs once")
	assert.Contains(t, h.logs[0], devices[0])
}

func TestEventWithoutRoute(t *testing.T) {
	t.Parallel()

	b, h, signal := newTest(t, alice)
	err := b.onEvent(h, pluginsdk.ChatEvent{ExternalID: bob, Messages: []pluginsdk.ChatText{{ID: 1, Text: "hi"}}})
	require.ErrorContains(t, err, "is not a linked device")
	assert.Empty(t, signal.sent)
}

func TestEventRetrySkipsSentReplies(t *testing.T) {
	t.Parallel()

	b, h, signal := newTest(t, alice)
	ev := pluginsdk.ChatEvent{ExternalID: alice, Title: "T", Messages: []pluginsdk.ChatText{{ID: 1, Text: "one"}, {ID: 2, Text: "two"}}}
	fail := true
	h.signal = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/send" && len(signal.sent) == 1 && fail {
			fail = false
			http.Error(rw, "rate limited", http.StatusTooManyRequests)
			return
		}
		signal.ServeHTTP(rw, r)
	})

	require.Error(t, b.onEvent(h, ev))
	require.NoError(t, b.onEvent(h, ev))
	assert.Equal(t, []string{
		alice + ">" + alice + ": " + replyMarker + "[T] one",
		alice + ">" + alice + ": " + replyMarker + "[T] two",
	}, signal.sentTexts())
}

func TestConnect(t *testing.T) {
	t.Parallel()

	b, h, signal := newTest(t)
	prompt, err := b.onConnect(h)
	require.NoError(t, err)
	assert.Equal(t, qrCodePNG, prompt.QRCodePNG)
	assert.Equal(t, connectInstructions, prompt.Instructions)

	signal.linkError = true
	_, err = b.onConnect(h)
	require.ErrorContains(t, err, "signal-cli exited")
}
