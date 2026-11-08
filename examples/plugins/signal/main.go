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
// Users reach the plugin in one of two ways:
//
//   - Bot account. The "account" config key names a phone number that is
//     registered with signal-cli-rest-api. Users message that number.
//   - Own account. A user adds the server as a linked device of their own
//     Signal account and writes to Note to Self. Every account on the server
//     other than the bot account is such a device.
//
// A user makes a link code in Coder and sends "/link CODE" to the bot or to
// Note to Self. Each later Signal message goes to the chat that last replied
// to the user, or to a new chat. "/new TEXT" always starts a new chat.
// Replies of the agent come back prefixed with the chat title.
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
			ConfigKeys:  []string{accountKey},
			TickSeconds: 5,
		},
		OnTick:  b.onTick,
		OnEvent: b.onEvent,
	})
}

func main() {}
