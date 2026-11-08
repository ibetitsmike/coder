// Package pluginsdk is the guest SDK for Coder plugins. A plugin is a Go
// program built with GOOS=wasip1 GOARCH=wasm -buildmode=c-shared that calls
// Register from an init function. The main function of a plugin is never
// called.
package pluginsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"golang.org/x/xerrors"
	"google.golang.org/protobuf/proto"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
)

// Manifest describes a plugin and the capabilities it requests.
type Manifest struct {
	Name        string
	Version     string
	Events      []string
	Chats       []string
	HTTPOrigins []string
	ConfigKeys  []string
	TickSeconds uint32
	Webhook     bool
	// Connect shows the prompt of Plugin.OnConnect to users who link an
	// account in Coder.
	Connect bool
}

// ChatEvent is delivered to Plugin.OnEvent.
type ChatEvent struct {
	ChatID     string
	ExternalID string
	Status     string
	Title      string
	Messages   []ChatText
}

// ChatText is the text of one chat message.
type ChatText struct {
	ID   int64
	Text string
}

// ConnectPrompt is what Plugin.OnConnect returns. Coder shows it to a user
// who links an account, for example to add Coder as a linked device of the
// user's own account.
type ConnectPrompt struct {
	// QRCodePNG is a PNG image of a QR code that the user scans in the
	// external service.
	QRCodePNG []byte
	// Instructions tell the user how to use the QR code and how to finish
	// linking with the link code that Coder shows next to it.
	Instructions string
}

// HTTPRequest is an outbound request for Host.Fetch or an inbound webhook
// request for Plugin.OnHTTP.
type HTTPRequest struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// HTTPResponse is the response to an HTTPRequest.
type HTTPResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Host is the API coderd exposes to a plugin while one of its handlers runs.
// It must not be used after the handler returns.
type Host interface {
	// Config returns the value of a configuration key listed in
	// Manifest.ConfigKeys, or "" if it is unset or unavailable.
	Config(key string) string
	// ChatSend sends text to the chat that last spoke to externalID, or to a
	// new chat if there is none, and returns the chat ID.
	ChatSend(externalID, text string) (chatID string, err error)
	// ChatSendNew always sends text to a new chat.
	ChatSendNew(externalID, text string) (chatID string, err error)
	// LinkComplete links externalID to the Coder user that issued code.
	LinkComplete(externalID, code string) (username string, err error)
	// Fetch sends an HTTP request to one of Manifest.HTTPOrigins.
	Fetch(req HTTPRequest) (HTTPResponse, error)
	// GetJSON fetches url and decodes a JSON response into v.
	GetJSON(url string, v any) error
	// PostJSON posts body as JSON to url and decodes a JSON response into v
	// when v is not nil.
	PostJSON(url string, body, v any) error
	// Logf writes a line to the plugin log in coderd.
	Logf(format string, args ...any)
}

// Plugin is the set of handlers a plugin implements. Nil handlers are
// no-ops, a nil OnHTTP responds with 404, and a nil OnConnect fails.
type Plugin struct {
	Manifest  Manifest
	OnTick    func(Host) error
	OnEvent   func(Host, ChatEvent) error
	OnHTTP    func(Host, HTTPRequest) HTTPResponse
	OnConnect func(Host) (ConnectPrompt, error)
}

// registered is read by the wasip1 exports.
var registered *Plugin //nolint:unused // Only read in GOOS=wasip1 builds.

// Register sets the plugin served by this module. Call it from init.
func Register(p Plugin) {
	registered = &p
}

// hostTransport sends one HostCall to coderd.
type hostTransport func(call *pluginproto.HostCall) (*pluginproto.HostResult, error)

type host struct {
	send hostTransport
}

func (h host) call(call *pluginproto.HostCall) (*pluginproto.HostResult, error) {
	res, err := h.send(call)
	if err != nil {
		return nil, err
	}
	if res.GetError() != "" {
		return nil, xerrors.New(res.GetError())
	}
	return res, nil
}

func (h host) Config(key string) string {
	res, err := h.call(&pluginproto.HostCall{Call: &pluginproto.HostCall_ConfigGet{ConfigGet: key}})
	if err != nil {
		return ""
	}
	return res.GetConfigValue()
}

func (h host) ChatSend(externalID, text string) (string, error) {
	return h.sendChat(externalID, text, false)
}

func (h host) ChatSendNew(externalID, text string) (string, error) {
	return h.sendChat(externalID, text, true)
}

func (h host) sendChat(externalID, text string, newChat bool) (string, error) {
	res, err := h.call(&pluginproto.HostCall{Call: &pluginproto.HostCall_ChatSend{ChatSend: &pluginproto.ChatSend{
		ExternalId: externalID,
		Text:       text,
		NewChat:    newChat,
	}}})
	if err != nil {
		return "", err
	}
	return res.GetChatSend().GetChatId(), nil
}

func (h host) LinkComplete(externalID, code string) (string, error) {
	res, err := h.call(&pluginproto.HostCall{Call: &pluginproto.HostCall_LinkComplete{LinkComplete: &pluginproto.LinkComplete{
		ExternalId: externalID,
		Code:       code,
	}}})
	if err != nil {
		return "", err
	}
	return res.GetLinkComplete().GetUsername(), nil
}

func (h host) Fetch(req HTTPRequest) (HTTPResponse, error) {
	res, err := h.call(&pluginproto.HostCall{Call: &pluginproto.HostCall_HttpFetch{HttpFetch: requestToProto(req)}})
	if err != nil {
		return HTTPResponse{}, err
	}
	return responseFromProto(res.GetHttpFetch()), nil
}

func (h host) GetJSON(url string, v any) error {
	return h.doJSON(HTTPRequest{
		Method: http.MethodGet,
		URL:    url,
		Header: http.Header{"Accept": {"application/json"}},
	}, v)
}

func (h host) PostJSON(url string, body, v any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return xerrors.Errorf("encode request body: %w", err)
	}
	return h.doJSON(HTTPRequest{
		Method: http.MethodPost,
		URL:    url,
		Header: http.Header{"Accept": {"application/json"}, "Content-Type": {"application/json"}},
		Body:   data,
	}, v)
}

func (h host) doJSON(req HTTPRequest, v any) error {
	res, err := h.Fetch(req)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		body := res.Body
		if len(body) > 512 {
			body = body[:512]
		}
		return xerrors.Errorf("%s %s: unexpected status %d: %s", req.Method, req.URL, res.StatusCode, bytes.TrimSpace(body))
	}
	if v == nil || len(res.Body) == 0 {
		return nil
	}
	if err := json.Unmarshal(res.Body, v); err != nil {
		return xerrors.Errorf("decode response from %s: %w", req.URL, err)
	}
	return nil
}

func (h host) Logf(format string, args ...any) {
	msg := validUTF8(fmt.Sprintf(format, args...))
	res, err := h.send(&pluginproto.HostCall{Call: &pluginproto.HostCall_Log{Log: &pluginproto.LogLine{
		Level:   pluginproto.LogLine_LEVEL_INFO,
		Message: msg,
	}}})
	if err != nil || res.GetError() != "" {
		// The sandbox still captures stderr when the host rejects the line.
		_, _ = fmt.Fprintln(os.Stderr, msg)
	}
}

func handleManifest(p *Plugin) []byte {
	var m Manifest
	if p != nil {
		m = p.Manifest
	}
	return mustMarshal(&pluginproto.Manifest{
		Name:        m.Name,
		Version:     m.Version,
		Events:      m.Events,
		Chats:       m.Chats,
		HttpOrigins: m.HTTPOrigins,
		TickSeconds: m.TickSeconds,
		Webhook:     m.Webhook,
		ConfigKeys:  m.ConfigKeys,
		Connect:     m.Connect,
	})
}

func handleTick(p *Plugin, send hostTransport) []byte {
	return resultBytes(func() error {
		if p == nil || p.OnTick == nil {
			return nil
		}
		return p.OnTick(host{send: send})
	})
}

func handleEvent(p *Plugin, send hostTransport, input []byte) []byte {
	return resultBytes(func() error {
		var ev pluginproto.ChatEvent
		if err := proto.Unmarshal(input, &ev); err != nil {
			return xerrors.Errorf("decode chat event: %w", err)
		}
		if p == nil || p.OnEvent == nil {
			return nil
		}
		event := ChatEvent{
			ChatID:     ev.GetChatId(),
			ExternalID: ev.GetExternalId(),
			Status:     ev.GetStatus(),
			Title:      ev.GetTitle(),
		}
		for _, msg := range ev.GetMessages() {
			event.Messages = append(event.Messages, ChatText{ID: msg.GetId(), Text: msg.GetText()})
		}
		return p.OnEvent(host{send: send}, event)
	})
}

func handleHTTP(p *Plugin, send hostTransport, input []byte) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			out = mustMarshal(&pluginproto.HTTPResponse{
				Status: http.StatusInternalServerError,
				Body:   fmt.Appendf(nil, "plugin panic: %v", r),
			})
		}
	}()
	var req pluginproto.HTTPRequest
	if err := proto.Unmarshal(input, &req); err != nil {
		return mustMarshal(&pluginproto.HTTPResponse{
			Status: http.StatusBadRequest,
			Body:   fmt.Appendf(nil, "decode request: %v", err),
		})
	}
	if p == nil || p.OnHTTP == nil {
		return mustMarshal(&pluginproto.HTTPResponse{Status: http.StatusNotFound})
	}
	res := p.OnHTTP(host{send: send}, requestFromProto(&req))
	if res.StatusCode == 0 {
		res.StatusCode = http.StatusOK
	}
	out, err := proto.Marshal(responseToProto(res))
	if err != nil {
		return mustMarshal(&pluginproto.HTTPResponse{
			Status: http.StatusInternalServerError,
			Body:   fmt.Appendf(nil, "encode response: %v", err),
		})
	}
	return out
}

func handleConnect(p *Plugin, send hostTransport) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			out = mustMarshal(&pluginproto.ConnectPrompt{Error: validUTF8(fmt.Sprintf("plugin panic: %v\n%s", r, debug.Stack()))})
		}
	}()
	if p == nil || p.OnConnect == nil {
		return mustMarshal(&pluginproto.ConnectPrompt{Error: "the plugin has no connect handler"})
	}
	prompt, err := p.OnConnect(host{send: send})
	if err != nil {
		msg := validUTF8(err.Error())
		if msg == "" {
			msg = "plugin returned an error with an empty message"
		}
		return mustMarshal(&pluginproto.ConnectPrompt{Error: msg})
	}
	return mustMarshal(&pluginproto.ConnectPrompt{
		QrCodePng:    prompt.QRCodePNG,
		Instructions: validUTF8(prompt.Instructions),
	})
}

// validUTF8 makes s safe for a proto3 string field, which must be UTF-8.
func validUTF8(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
}

func resultBytes(fn func() error) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			out = mustMarshal(&pluginproto.Result{Error: validUTF8(fmt.Sprintf("plugin panic: %v\n%s", r, debug.Stack()))})
		}
	}()
	var res pluginproto.Result
	if err := fn(); err != nil {
		res.Error = validUTF8(err.Error())
		if res.Error == "" {
			res.Error = "plugin returned an error with an empty message"
		}
	}
	return mustMarshal(&res)
}

func mustMarshal(m proto.Message) []byte {
	data, err := proto.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("marshal %T: %v", m, err))
	}
	return data
}

func requestToProto(req HTTPRequest) *pluginproto.HTTPRequest {
	return &pluginproto.HTTPRequest{
		Method:  req.Method,
		Url:     req.URL,
		Headers: headersToProto(req.Header),
		Body:    req.Body,
	}
}

func requestFromProto(req *pluginproto.HTTPRequest) HTTPRequest {
	return HTTPRequest{
		Method: req.GetMethod(),
		URL:    req.GetUrl(),
		Header: headersFromProto(req.GetHeaders()),
		Body:   req.GetBody(),
	}
}

func responseToProto(res HTTPResponse) *pluginproto.HTTPResponse {
	return &pluginproto.HTTPResponse{
		Status:  int32(res.StatusCode), //nolint:gosec // HTTP status codes fit in int32.
		Headers: headersToProto(res.Header),
		Body:    res.Body,
	}
}

func responseFromProto(res *pluginproto.HTTPResponse) HTTPResponse {
	return HTTPResponse{
		StatusCode: int(res.GetStatus()),
		Header:     headersFromProto(res.GetHeaders()),
		Body:       res.GetBody(),
	}
}

func headersToProto(h http.Header) []*pluginproto.Header {
	var out []*pluginproto.Header
	for name, values := range h {
		for _, value := range values {
			out = append(out, &pluginproto.Header{Name: name, Value: value})
		}
	}
	return out
}

func headersFromProto(headers []*pluginproto.Header) http.Header {
	h := make(http.Header, len(headers))
	for _, header := range headers {
		h.Add(header.GetName(), header.GetValue())
	}
	return h
}
