package main

import (
	"errors"
	"slices"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
)

const (
	accountKey = "account"

	botHelp = "Send /link CODE with a code from Coder to link this number. " +
		"Then send any text to talk to your agent, or /new TEXT to start a new chat."
	deviceHelp = "Send /link CODE here with a code from Coder to link this account. " +
		"Then send any text here to talk to your agent, or /new TEXT to start a new chat."
	welcomeText = "Coder is now a linked device of this account. " + deviceHelp

	// replyMarker starts each message that the plugin writes to a Note to
	// Self chat. The user and the plugin write there as the same account,
	// so the marker shows who wrote what. The plugin also skips marked
	// messages: two deployments linked to one account would otherwise
	// answer each other's replies forever.
	replyMarker = "🤖 "

	// maxReceivesPerTick bounds the receive calls of one tick. In normal and
	// native mode each call takes at least the one second receive timeout,
	// and a tick must finish within the 10 second sandbox call timeout.
	maxReceivesPerTick = 4

	maxSent = 1000
)

// sentKey identifies a reply that went out. coderd retries a whole event when
// one send fails, so the plugin skips the replies of the event that already
// went out.
type sentKey struct {
	recipient string
	messageID int64
}

// bridge holds the state of the plugin between calls. coderd calls one
// handler at a time, so it needs no lock.
type bridge struct {
	apiURL string
	// devices are the accounts on the server other than the bot account,
	// sorted by ID. Each one is a Signal account that Coder is a linked
	// device of, and its owner talks to the plugin in Note to Self.
	devices []account
	// known has the ID of each device. It is nil until the first list, so
	// devices that existed before the plugin started get no welcome.
	known map[string]bool
	// next is the index in devices where the next tick starts to poll.
	next int
	// receiveErrors has the last receive error of each device, so a device
	// that keeps failing logs once.
	receiveErrors map[string]string
	sent          map[sentKey]struct{}
}

func newBridge(apiURL string) *bridge {
	return &bridge{
		apiURL:        trimBaseURL(apiURL),
		receiveErrors: map[string]string{},
		sent:          map[sentKey]struct{}{},
	}
}

func (b *bridge) api(h pluginsdk.Host) signalAPI {
	return signalAPI{h: h, baseURL: b.apiURL}
}

// onTick polls the bot account and the devices for new messages and routes
// each one.
func (b *bridge) onTick(h pluginsdk.Host) error {
	api := b.api(h)
	bot := h.Config(accountKey)
	var errs []error
	if err := b.refreshDevices(h, api, bot); err != nil {
		errs = append(errs, err)
	}
	budget := maxReceivesPerTick
	if bot != "" {
		budget--
		msgs, err := api.receive(bot)
		if err != nil {
			errs = append(errs, xerrors.Errorf("receive for bot account %s: %w", bot, err))
		}
		for _, m := range msgs {
			b.handleBotMessage(h, api, bot, m)
		}
	}
	for _, d := range b.devicesToPoll(budget) {
		msgs, err := api.receive(d.id())
		b.noteReceiveError(h, d.id(), err)
		for _, m := range msgs {
			b.handleDeviceMessage(h, api, d, m)
		}
	}
	return errors.Join(errs...)
}

// refreshDevices lists the accounts on the server and welcomes each device
// that appeared since the last list.
func (b *bridge) refreshDevices(h pluginsdk.Host, api signalAPI, bot string) error {
	accounts, err := api.accounts()
	if err != nil {
		return err
	}
	greet := b.known != nil
	devices := make([]account, 0, len(accounts))
	known := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		id := a.id()
		if id == "" || id == bot || known[id] {
			continue
		}
		known[id] = true
		devices = append(devices, a)
		if greet && !b.known[id] {
			if err := b.sendToSelf(api, id, welcomeText); err != nil {
				h.Logf("welcome %s: %v", id, err)
			}
		}
	}
	slices.SortFunc(devices, func(x, y account) int { return strings.Compare(x.id(), y.id()) })
	b.devices, b.known = devices, known
	return nil
}

// devicesToPoll returns up to n devices, starting where the last tick
// stopped, so every device gets polled when there are more than n.
func (b *bridge) devicesToPoll(n int) []account {
	if len(b.devices) <= n {
		return b.devices
	}
	out := make([]account, 0, n)
	for i := range n {
		out = append(out, b.devices[(b.next+i)%len(b.devices)])
	}
	b.next = (b.next + n) % len(b.devices)
	return out
}

func (b *bridge) noteReceiveError(h pluginsdk.Host, id string, err error) {
	if err == nil {
		delete(b.receiveErrors, id)
		return
	}
	if b.receiveErrors[id] == err.Error() {
		return
	}
	b.receiveErrors[id] = err.Error()
	h.Logf("receive for %s: %v", id, err)
}

// handleBotMessage handles a direct message to the bot account.
func (*bridge) handleBotMessage(h pluginsdk.Host, api signalAPI, bot string, m receivedMessage) {
	env := m.Envelope
	if env.DataMessage == nil || len(env.DataMessage.GroupInfo) > 0 {
		return
	}
	sender := env.SourceNumber
	if sender == "" {
		sender = env.SourceUUID
	}
	text := strings.TrimSpace(env.DataMessage.Message)
	if sender == "" || text == "" {
		return
	}
	if reply := handleText(h, sender, text, botHelp); reply != "" {
		if err := api.send(bot, sender, reply); err != nil {
			h.Logf("reply to %s: %v", sender, err)
		}
	}
}

// handleDeviceMessage handles a message that the owner of a device wrote to
// Note to Self on another device. The plugin ignores every other message of
// the account: those are the owner's private conversations.
func (b *bridge) handleDeviceMessage(h pluginsdk.Host, api signalAPI, d account, m receivedMessage) {
	sync := m.Envelope.SyncMessage
	if sync == nil || sync.SentMessage == nil || !sync.SentMessage.toSelf(d) {
		return
	}
	text := strings.TrimSpace(sync.SentMessage.Message)
	if text == "" || strings.HasPrefix(text, strings.TrimSpace(replyMarker)) {
		return
	}
	if reply := handleText(h, d.id(), text, deviceHelp); reply != "" {
		if err := b.sendToSelf(api, d.id(), reply); err != nil {
			h.Logf("reply to %s: %v", d.id(), err)
		}
	}
}

// handleText runs a command or sends text to a chat, and returns the text to
// send back to the user, if any.
func handleText(h pluginsdk.Host, externalID, text, help string) string {
	command, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)
	switch strings.ToLower(command) {
	case "/help":
		return help
	case "/link":
		username, err := h.LinkComplete(externalID, arg)
		if err != nil {
			return "Could not link: " + err.Error()
		}
		return "Linked to Coder user " + username + ". " + help
	case "/new":
		if arg == "" {
			return "Send /new TEXT to start a new chat."
		}
		if _, err := h.ChatSendNew(externalID, arg); err != nil {
			return "Could not start a chat: " + err.Error() + "\n" + help
		}
		return ""
	default:
		if _, err := h.ChatSend(externalID, text); err != nil {
			return "Could not send: " + err.Error() + "\n" + help
		}
		return ""
	}
}

// onEvent sends the replies of the agent to the linked Signal account: to
// Note to Self when Coder is a linked device of the account, and from the
// bot account otherwise.
func (b *bridge) onEvent(h pluginsdk.Host, ev pluginsdk.ChatEvent) error {
	api := b.api(h)
	bot := h.Config(accountKey)
	// coderd delivers missed events before the first tick.
	if b.known == nil {
		if err := b.refreshDevices(h, api, bot); err != nil {
			return err
		}
	}
	send := func(text string) error {
		if b.known[ev.ExternalID] {
			return b.sendToSelf(api, ev.ExternalID, text)
		}
		if bot == "" {
			return xerrors.Errorf("%s is not a linked device and config key %q is not set", ev.ExternalID, accountKey)
		}
		return api.send(bot, ev.ExternalID, text)
	}

	title := ev.Title
	if title == "" {
		title = "New chat"
	}
	for _, m := range ev.Messages {
		key := sentKey{recipient: ev.ExternalID, messageID: m.ID}
		if _, ok := b.sent[key]; ok {
			continue
		}
		if err := send("[" + title + "] " + m.Text); err != nil {
			return err
		}
		if len(b.sent) >= maxSent {
			clear(b.sent)
		}
		b.sent[key] = struct{}{}
	}
	if len(ev.Messages) == 0 && ev.Status == "error" {
		return send("[" + title + "] The chat stopped with an error. Open it in Coder for details.")
	}
	return nil
}

func (*bridge) sendToSelf(api signalAPI, id, text string) error {
	return api.send(id, id, replyMarker+text)
}
