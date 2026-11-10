package chatstate

// This file is fork code: it lets fork features post a notice into a chat's
// history, which upstream has no transition for.

import (
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
)

// ForkAppendNotice appends message, a user-visible system notice, to the
// chat's history. It changes neither the execution state nor the archived
// flag, so it starts no turn and works on an archived chat.
func (tx *Tx) ForkAppendNotice(message Message) error {
	if message.Role != database.ChatMessageRoleSystem || message.Visibility != database.ChatMessageVisibilityUser {
		return xerrors.New("a notice must be a user-visible system message")
	}
	_, err := tx.insertMessages([]Message{message})
	return err
}
