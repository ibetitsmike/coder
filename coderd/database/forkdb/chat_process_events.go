package forkdb

import (
	"encoding/json"
	"time"

	"github.com/sqlc-dev/pqtype"
	"golang.org/x/xerrors"
)

// ChatProcessFired is the fired_payload of a process watch. Exit or Lost ends
// the watch, and the payload keeps beside it the output lines that matched
// the pattern of the watch since its last delivery and were still held back
// when the exit or loss merged in: Matched counts them and Lines keeps the
// last of them, which the delivery folds into the exit or loss message.
// Without an exit or a loss, the payload holds such lines alone.
// FireChatProcessWatches and MarkLostChatProcessWatches write it.
type ChatProcessFired struct {
	Exit    *ChatProcessExit `json:"exit,omitempty"`
	Lost    *ChatProcessLost `json:"lost,omitempty"`
	Matched int              `json:"matched,omitempty"`
	Lines   string           `json:"lines,omitempty"`
}

// ChatProcessExit is how a watched process exited: its exit code, -1 when a
// signal killed it, the end of its output, and when it exited.
type ChatProcessExit struct {
	Code int       `json:"code"`
	Tail string    `json:"tail,omitempty"`
	At   time.Time `json:"at"`
}

// ChatProcessLost tells why and since when a watched process is gone without
// an exit that anyone saw.
type ChatProcessLost struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// ChatProcessExitEnded returns the @ended argument of FireChatProcessWatches
// for exit.
func ChatProcessExitEnded(exit ChatProcessExit) (pqtype.NullRawMessage, error) {
	return chatProcessEnded(ChatProcessFired{Exit: &exit})
}

// ChatProcessLostEnded returns the @ended argument of FireChatProcessWatches
// for lost.
func ChatProcessLostEnded(lost ChatProcessLost) (pqtype.NullRawMessage, error) {
	return chatProcessEnded(ChatProcessFired{Lost: &lost})
}

func chatProcessEnded(fired ChatProcessFired) (pqtype.NullRawMessage, error) {
	raw, err := json.Marshal(fired)
	if err != nil {
		return pqtype.NullRawMessage{}, xerrors.Errorf("encode process event: %w", err)
	}
	return pqtype.NullRawMessage{RawMessage: raw, Valid: true}, nil
}
