// Command bridge is a test plugin for coderd integration tests. Each webhook
// request carries one JSON operation, so a test drives every host call
// through the real HTTP API and reads the result from the response.
package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
)

type request struct {
	Op         string `json:"op"`
	ExternalID string `json:"external_id"`
	Text       string `json:"text"`
	Code       string `json:"code"`
	NewChat    bool   `json:"new_chat"`
	URL        string `json:"url"`
	Key        string `json:"key"`
}

type response struct {
	Error  string                `json:"error,omitempty"`
	Value  string                `json:"value,omitempty"`
	Status int                   `json:"status,omitempty"`
	Body   string                `json:"body,omitempty"`
	Events []pluginsdk.ChatEvent `json:"events,omitempty"`
	Ticks  int                   `json:"ticks,omitempty"`
}

var (
	events []pluginsdk.ChatEvent
	ticks  int
)

func init() {
	pluginsdk.Register(pluginsdk.Plugin{
		Manifest: pluginsdk.Manifest{
			Name:        "bridge",
			Version:     "1.0.0",
			Events:      []string{"chat.messages_added", "chat.status_changed"},
			Chats:       []string{"create", "send", "read"},
			HTTPOrigins: []string{"http://127.0.0.1:9"},
			ConfigKeys:  []string{"token"},
			TickSeconds: 2,
			Webhook:     true,
		},
		OnTick: func(pluginsdk.Host) error {
			ticks++
			return nil
		},
		OnEvent: func(h pluginsdk.Host, ev pluginsdk.ChatEvent) error {
			events = append(events, ev)
			h.Logf("bridge-event external_id=%s chat_id=%s", ev.ExternalID, ev.ChatID)
			return nil
		},
		OnHTTP: onHTTP,
	})
}

func main() {}

func onHTTP(h pluginsdk.Host, req pluginsdk.HTTPRequest) pluginsdk.HTTPResponse {
	var in request
	if err := json.Unmarshal(req.Body, &in); err != nil {
		return reply(http.StatusBadRequest, response{Error: err.Error()})
	}
	var out response
	var err error
	switch in.Op {
	case "link":
		out.Value, err = h.LinkComplete(in.ExternalID, in.Code)
	case "send":
		if in.NewChat {
			out.Value, err = h.ChatSendNew(in.ExternalID, in.Text)
		} else {
			out.Value, err = h.ChatSend(in.ExternalID, in.Text)
		}
	case "fetch":
		var res pluginsdk.HTTPResponse
		res, err = h.Fetch(pluginsdk.HTTPRequest{Method: http.MethodGet, URL: in.URL})
		out.Status, out.Body = res.StatusCode, string(res.Body)
	case "config":
		out.Value = h.Config(in.Key)
	case "events":
		out.Events = events
	case "ticks":
		out.Ticks = ticks
	case "request":
		out.Value, out.Body = req.URL, strings.Join(req.Header.Values(in.Key), ",")
	default:
		return reply(http.StatusBadRequest, response{Error: "unknown op " + in.Op})
	}
	if err != nil {
		out.Error = err.Error()
	}
	return reply(http.StatusOK, out)
}

func reply(status int, out response) pluginsdk.HTTPResponse {
	body, _ := json.Marshal(out)
	return pluginsdk.HTTPResponse{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       body,
	}
}
