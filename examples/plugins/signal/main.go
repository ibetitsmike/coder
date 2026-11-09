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
// the manifest an owner approves. Set the "account" config key to the phone
// number that signal-cli-rest-api is registered with.
//
// A user makes a link code in Coder and sends "/link CODE" to the bot. Each
// later Signal message goes to the chat that last replied to the user, or to
// a new chat. "/new TEXT" always starts a new chat. Replies of the agent come
// back prefixed with the chat title.
package main

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
)

// apiURL is the base URL of signal-cli-rest-api. Override it at build time.
var apiURL = "http://signal-cli-rest-api:8080"

const (
	accountKey = "account"
	helpText   = "Send /link CODE with a code from Coder to link this number. " +
		"Then send any text to talk to your agent, or /new TEXT to start a new chat."
)

// receivedMessage is one item of GET /v1/receive/{number}.
type receivedMessage struct {
	Envelope struct {
		SourceNumber string `json:"sourceNumber"`
		SourceUUID   string `json:"sourceUuid"`
		DataMessage  *struct {
			Message string `json:"message"`
		} `json:"dataMessage"`
	} `json:"envelope"`
}

// sentKey identifies a reply that went out. coderd retries a whole event when
// one send fails, so the plugin skips the replies of the event that already
// went out.
type sentKey struct {
	recipient string
	messageID int64
}

const maxSent = 1000

var sent = map[sentKey]struct{}{}

type sendRequest struct {
	Message    string   `json:"message"`
	Number     string   `json:"number"`
	Recipients []string `json:"recipients"`
}

func init() {
	origin := apiURL
	if u, err := url.Parse(apiURL); err == nil {
		origin = u.Scheme + "://" + u.Host
	}
	pluginsdk.Register(pluginsdk.Plugin{
		Manifest: pluginsdk.Manifest{
			Name:        "signal",
			Version:     "0.1.0",
			Events:      []string{"chat.messages_added", "chat.status_changed"},
			Chats:       []string{"create", "send", "read"},
			HTTPOrigins: []string{origin},
			ConfigKeys:  []string{accountKey},
			TickSeconds: 5,
		},
		OnTick:  onTick,
		OnEvent: onEvent,
	})
}

func main() {}

// onTick polls signal-cli-rest-api for new messages and routes each one.
func onTick(h pluginsdk.Host) error {
	account := h.Config(accountKey)
	if account == "" {
		return xerrors.Errorf("config key %q is not set", accountKey)
	}
	var received []receivedMessage
	err := h.GetJSON(fmt.Sprintf("%s/v1/receive/%s?timeout=1&ignore_attachments=true&ignore_stories=true",
		strings.TrimSuffix(apiURL, "/"), url.PathEscape(account)), &received)
	if err != nil {
		return err
	}
	for _, m := range received {
		env := m.Envelope
		if env.DataMessage == nil {
			continue
		}
		sender := env.SourceNumber
		if sender == "" {
			sender = env.SourceUUID
		}
		text := strings.TrimSpace(env.DataMessage.Message)
		if sender == "" || text == "" {
			continue
		}
		if reply := handleText(h, sender, text); reply != "" {
			if err := send(h, account, sender, reply); err != nil {
				h.Logf("reply to %s: %v", sender, err)
			}
		}
	}
	return nil
}

// handleText runs a command or sends text to a chat, and returns the text to
// send back to the user, if any.
func handleText(h pluginsdk.Host, sender, text string) string {
	command, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)
	switch strings.ToLower(command) {
	case "/help":
		return helpText
	case "/link":
		username, err := h.LinkComplete(sender, arg)
		if err != nil {
			return "Could not link: " + err.Error()
		}
		return "Linked to Coder user " + username + ". " + helpText
	case "/new":
		if arg == "" {
			return "Send /new TEXT to start a new chat."
		}
		if _, err := h.ChatSendNew(sender, arg); err != nil {
			return "Could not start a chat: " + err.Error() + "\n" + helpText
		}
		return ""
	default:
		if _, err := h.ChatSend(sender, text); err != nil {
			return "Could not send: " + err.Error() + "\n" + helpText
		}
		return ""
	}
}

// onEvent sends the replies of the agent to the linked Signal account.
func onEvent(h pluginsdk.Host, ev pluginsdk.ChatEvent) error {
	account := h.Config(accountKey)
	if account == "" {
		return xerrors.Errorf("config key %q is not set", accountKey)
	}
	title := ev.Title
	if title == "" {
		title = "New chat"
	}
	for _, m := range ev.Messages {
		key := sentKey{recipient: ev.ExternalID, messageID: m.ID}
		if _, ok := sent[key]; ok {
			continue
		}
		if err := send(h, account, ev.ExternalID, "["+title+"] "+m.Text); err != nil {
			return err
		}
		if len(sent) >= maxSent {
			clear(sent)
		}
		sent[key] = struct{}{}
	}
	if len(ev.Messages) == 0 && ev.Status == "error" {
		return send(h, account, ev.ExternalID, "["+title+"] The chat stopped with an error. Open it in Coder for details.")
	}
	return nil
}

func send(h pluginsdk.Host, account, recipient, text string) error {
	return h.PostJSON(strings.TrimSuffix(apiURL, "/")+"/v2/send", sendRequest{
		Message:    text,
		Number:     account,
		Recipients: []string{recipient},
	}, nil)
}
