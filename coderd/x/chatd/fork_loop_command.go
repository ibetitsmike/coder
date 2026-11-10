package chatd

// This file is fork code: it holds the /loop chat command of the
// fork-chat-wakeups experiment. SendMessage calls forkLoopCommand before it
// stores a message and schedules the loop in the same transaction.

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
)

const defaultLoopInterval = 10 * time.Minute

var (
	loopIntervalPattern     = regexp.MustCompile(`^([0-9]+)(s|m|h|d)$`)
	loopIntervalUnitSeconds = map[string]int{"s": 1, "m": 60, "h": 3600, "d": 86400}
)

// forkLoop is a parsed /loop command whose wake-up SendMessage creates with
// the message.
type forkLoop struct {
	server *Server
	spec   wakeupSpec
}

// forkLoopCommand parses a /loop command. It returns the message parts to
// store, which hold only the prompt, and the loop to schedule. Any other
// message, or any message while the experiment is off, comes back unchanged
// with a nil loop.
func (p *Server) forkLoopCommand(parts []codersdk.ChatMessagePart) ([]codersdk.ChatMessagePart, *forkLoop, error) {
	if !p.experiments.Enabled(codersdk.ExperimentForkChatWakeups) ||
		len(parts) != 1 || parts[0].Type != codersdk.ChatMessagePartTypeText {
		return parts, nil, nil
	}
	spec, ok, err := parseLoopCommand(parts[0].Text)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return parts, nil, nil
	}
	part := parts[0]
	part.Text = spec.prompt
	return []codersdk.ChatMessagePart{part}, &forkLoop{server: p, spec: spec}, nil
}

// schedule creates the interval wake-up of the loop in store, the transaction
// that stores its first message. It does nothing for a nil loop.
func (l *forkLoop) schedule(ctx context.Context, store database.Store, chat database.Chat, now time.Time) error {
	if l == nil {
		return nil
	}
	_, err := l.server.createChatWakeup(ctx, store, chat, l.spec, now)
	return err
}

// parseLoopCommand parses "/loop [interval] <prompt>". ok is false when text
// is not a /loop command. The interval defaults to defaultLoopInterval when
// the first word is not one.
func parseLoopCommand(text string) (wakeupSpec, bool, error) {
	text = strings.TrimSpace(text)
	if !isLoopCommand(text) {
		return wakeupSpec{}, false, nil
	}
	usage := xerrors.Errorf("usage: %s [interval] <prompt>", loopCommand)
	rest := strings.TrimSpace(text[len(loopCommand):])
	if rest == "" {
		return wakeupSpec{}, true, usage
	}

	intervalSeconds := int(defaultLoopInterval / time.Second)
	first, after := rest, ""
	if end := strings.IndexFunc(rest, unicode.IsSpace); end >= 0 {
		first, after = rest[:end], rest[end:]
	}
	if match := loopIntervalPattern.FindStringSubmatch(first); match != nil {
		count, err := strconv.Atoi(match[1])
		if err != nil || count > int(maxWakeupPeriod/time.Second) {
			return wakeupSpec{}, true, xerrors.Errorf("interval %q is longer than %s", first, maxWakeupPeriod)
		}
		intervalSeconds = count * loopIntervalUnitSeconds[match[2]]
		rest = strings.TrimSpace(after)
		if rest == "" {
			return wakeupSpec{}, true, usage
		}
	}

	spec, err := wakeupSpec{
		source:          wakeupSourceLoop,
		kind:            forkdb.ChatWakeupKindInterval,
		prompt:          rest,
		intervalSeconds: intervalSeconds,
	}.validate()
	return spec, true, err
}

// isLoopCommand reports whether text starts with the /loop command word.
func isLoopCommand(text string) bool {
	rest, ok := strings.CutPrefix(text, loopCommand)
	if !ok {
		return false
	}
	next, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || unicode.IsSpace(next)
}
