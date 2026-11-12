package chatd

// This file is fork code: it holds the message_family_chat tool of the
// fork-family-messages experiment. appendForkTools adds the tool to a turn.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

const (
	messageFamilyChatToolName = "message_family_chat"
	maxFamilyMessagesPerHour  = 60
	maxFamilyMessageBytes     = 16384
)

type messageFamilyChatArgs struct {
	ChatID  string `json:"chat_id" description:"The ID of the chat to message: your parent, a sibling, or another chat under the same root."`
	Message string `json:"message" description:"The message text."`
}

// familyMessageResult keeps its fields in sorted JSON key order, like the
// subagent tool results.
type familyMessageResult struct {
	ChatID string `json:"chat_id"`
	Queued bool   `json:"queued,omitempty"`
	Status string `json:"status"`
	Title  string `json:"title"`
}

// familyMessageTools returns the message_family_chat tool, which sends a
// message from chat to another chat in its family.
func (p *Server) familyMessageTools(chat database.Chat) []fantasy.AgentTool {
	return []fantasy.AgentTool{serialFamilyMessageTool{AgentTool: fantasy.NewAgentTool(
		messageFamilyChatToolName,
		"Send a message to another chat in your chat family: your parent, a "+
			"sibling, or any other chat under the same root, by chat ID. If the "+
			"recipient is idle, it starts a new turn with the message. If it is "+
			"busy, the message is delivered before its next model call, and its "+
			"current work continues. The recipient sees your chat ID and title, "+
			"and that the message comes from an agent, not from the user. This "+
			"call does not wait for a reply; the recipient replies with "+
			messageFamilyChatToolName+". "+
			fmt.Sprintf("At most %d messages per chat per hour.", maxFamilyMessagesPerHour),
		func(ctx context.Context, args messageFamilyChatArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			target, queued, err := p.sendFamilyMessage(ctx, chat, args)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return toolJSONResponse(familyMessageResult{
				ChatID: target.ID.String(),
				Queued: queued,
				Status: string(target.Status),
				Title:  target.Title,
			}), nil
		},
	)}}
}

// serialFamilyMessageTool runs the message_family_chat calls of a step one at
// a time, so each call counts the messages of the calls before it and the
// hourly limit holds.
type serialFamilyMessageTool struct {
	fantasy.AgentTool
}

func (serialFamilyMessageTool) SerialToolCalls() bool { return true }

// sendFamilyMessage sends args.Message from sender to the chat args.ChatID of
// the same family, the way message_agent sends to a child: an idle recipient
// starts a turn, a busy one gets the message before its next model call, and
// no turn is interrupted. It returns the recipient after the send and whether
// the message was queued. A sender sends at most maxFamilyMessagesPerHour
// messages in the rolling hour.
func (p *Server) sendFamilyMessage(ctx context.Context, sender database.Chat, args messageFamilyChatArgs) (database.Chat, bool, error) {
	targetID, err := parseSubagentToolChatID(args.ChatID)
	if err != nil {
		return database.Chat{}, false, err
	}
	body := strings.TrimSpace(args.Message)
	if body == "" {
		return database.Chat{}, false, xerrors.New("message is required")
	}
	if len(body) > maxFamilyMessageBytes {
		return database.Chat{}, false, xerrors.Errorf("message is too long: at most %d bytes", maxFamilyMessageBytes)
	}
	if targetID == sender.ID {
		return database.Chat{}, false, xerrors.New("a chat cannot message itself")
	}

	// A missing chat and a chat of another family get the same error, so a
	// chat cannot probe which chat IDs exist outside its family.
	notInFamily := xerrors.Errorf("chat %s is not in this chat's family", targetID)
	target, err := p.db.GetChatByID(ctx, targetID)
	if errors.Is(err, sql.ErrNoRows) || dbauthz.IsNotAuthorizedError(err) {
		return database.Chat{}, false, notInFamily
	}
	if err != nil {
		return database.Chat{}, false, xerrors.Errorf("get target chat: %w", err)
	}
	if familyRootID(target) != familyRootID(sender) {
		return database.Chat{}, false, notInFamily
	}
	if target.Archived {
		return database.Chat{}, false, xerrors.Errorf("chat %s is archived", targetID)
	}
	// The title may have changed since the turn started.
	sender, err = p.db.GetChatByID(ctx, sender.ID)
	if err != nil {
		return database.Chat{}, false, xerrors.Errorf("get sender chat: %w", err)
	}

	fdb, err := forkdb.From(p.db)
	if err != nil {
		return database.Chat{}, false, xerrors.Errorf("fork queries: %w", err)
	}
	//nolint:gocritic // chatd counts its own bookkeeping rows.
	sent, err := fdb.CountChatFamilyMessagesSince(dbauthz.AsChatd(ctx), forkdb.CountChatFamilyMessagesSinceParams{
		SenderChatID: sender.ID,
		Since:        p.clock.Now().Add(-time.Hour),
	})
	if err != nil {
		return database.Chat{}, false, xerrors.Errorf("count family messages: %w", err)
	}
	if sent >= maxFamilyMessagesPerHour {
		return database.Chat{}, false, xerrors.Errorf("message limit reached: at most %d messages per chat per hour", maxFamilyMessagesPerHour)
	}

	// Like message_agent, a message resumes a finished child, so it counts
	// against the family live subagent limit.
	ctx = withSubagentResume(ctx)
	if err := p.forkValidateSubagentResume(ctx, p.db, target, false); err != nil {
		return database.Chat{}, false, err
	}
	result, err := p.SendMessage(ctx, SendMessageOptions{
		ChatID:       target.ID,
		CreatedBy:    target.OwnerID,
		Content:      []codersdk.ChatMessagePart{codersdk.ChatMessageText(familyMessageEnvelope(sender.ID, sender.Title, body))},
		BusyBehavior: SendMessageBusyBehaviorSteer,
		forkAdmit: func(ctx context.Context, store database.Store, _ chatstate.SendMessageResult) error {
			txdb, err := forkdb.From(store)
			if err != nil {
				return xerrors.Errorf("fork queries: %w", err)
			}
			//nolint:gocritic // chatd records its own bookkeeping rows.
			err = txdb.InsertChatFamilyMessage(dbauthz.AsChatd(ctx), forkdb.InsertChatFamilyMessageParams{
				ID:              uuid.New(),
				SenderChatID:    sender.ID,
				RecipientChatID: target.ID,
				CreatedAt:       p.clock.Now(),
			})
			if err != nil {
				return xerrors.Errorf("record family message: %w", err)
			}
			return nil
		},
	})
	if err != nil {
		return database.Chat{}, false, err
	}
	return result.Chat, result.Queued, nil
}

// familyRootID returns the root of the family of chat: its root chat, or the
// chat itself when it is a root.
func familyRootID(chat database.Chat) uuid.UUID {
	if chat.RootChatID.Valid {
		return chat.RootChatID.UUID
	}
	return chat.ID
}

// familyMessageEnvelope returns the user message text that delivers body from
// the sender chat. Clients parse this text to show the message as one from an
// agent, so its format must stay stable.
func familyMessageEnvelope(senderID uuid.UUID, senderTitle, body string) string {
	return fmt.Sprintf("<agent-message from_chat_id=\"%s\" from_title=%s>\n"+
		"This message comes from another agent in your chat family, not from the user. "+
		"To reply, call %s with chat_id \"%s\".\n\n"+
		"%s\n</agent-message>",
		senderID, strconv.Quote(senderTitle), messageFamilyChatToolName, senderID, strings.TrimSpace(body))
}
