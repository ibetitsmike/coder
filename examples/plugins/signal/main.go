// Command signal is a Coder plugin that bridges Coder chats to Signal through
// a signal-cli-rest-api server (https://github.com/bbernhard/signal-cli-rest-api)
// that runs in "normal" or "native" mode.
//
// Build it with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared \
//	    -ldflags "-X main.apiURL=http://signal-cli-rest-api:8080" \
//	    -o signal.wasm ./examples/plugins/signal
//
// The plugin can only reach the origin it is built with, so apiURL is part of
// the manifest an owner approves.
//
// Each user adds the server as a linked device of their own Signal account:
// Settings > Linked accounts in Coder shows a QR code from the server and a
// link code. The user scans the QR code in Signal and sends "/link CODE" to
// Note to Self. Each later message to Note to Self goes to the chat that last
// replied to the user, or to a new chat. "/new TEXT" always starts a new
// chat. Replies of the agent come back to Note to Self, prefixed with the
// chat title.
package main

import (
	"net/url"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
)

// apiURL is the base URL of signal-cli-rest-api. Override it at build time.
var apiURL = "http://signal-cli-rest-api:8080"

func init() {
	origin := apiURL
	if u, err := url.Parse(apiURL); err == nil {
		origin = u.Scheme + "://" + u.Host
	}
	b := newBridge(apiURL)
	pluginsdk.Register(pluginsdk.Plugin{
		Manifest: pluginsdk.Manifest{
			Name:        "signal",
			Version:     "0.2.0",
			Events:      []string{"chat.messages_added", "chat.status_changed"},
			Chats:       []string{"create", "send", "read"},
			HTTPOrigins: []string{origin},
			TickSeconds: 5,
			Connect:     true,
		},
		OnTick:    b.onTick,
		OnEvent:   b.onEvent,
		OnConnect: b.onConnect,
	})
}

func main() {}
