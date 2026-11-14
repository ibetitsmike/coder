package chatd

// This file is fork code: it makes a replayed message_agent call return
// without sending its message again, behind the fork-nested-subagents
// experiment. When chatd stops in the middle of a tool batch, the next worker
// runs the whole batch again with the same tool call IDs. Without this
// wrapper the child would get each message_agent message of that batch twice.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	messageAgentToolName = "message_agent"
	// subagentMessageRetention is how long a message_agent call is recorded.
	// A replay runs when the next worker acquires the chat, so a day covers
	// a chat that stays unacquired for a long time.
	subagentMessageRetention = 24 * time.Hour
)

// forkSubagentMessageKey carries a forkSubagentMessageCall into the send of a
// message_agent call, so that the send records the call in its transaction.
type forkSubagentMessageKey struct{}

// forkSubagentMessageCall is the tool call ID of a message_agent call and the
// organization of the sending chat.
type forkSubagentMessageCall struct {
	id             uuid.UUID
	organizationID uuid.UUID
}

// forkReplaySafeMessageAgentTool wraps message_agent so that a call with a
// tool call ID records that ID with its message, and a replay of the call
// returns the recipient without sending again.
type forkReplaySafeMessageAgentTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withForkReplaySafeMessageAgent wraps every message_agent tool in tools with
// forkReplaySafeMessageAgentTool.
func (p *Server) withForkReplaySafeMessageAgent(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	if !p.nestedSubagentsEnabled() {
		return tools
	}
	for i, tool := range tools {
		if tool.Info().Name == messageAgentToolName {
			tools[i] = forkReplaySafeMessageAgentTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

func (t forkReplaySafeMessageAgentTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	callID, ok := workspacesdk.ToolCallIDFromContext(ctx)
	if !ok {
		return t.AgentTool.Run(ctx, call)
	}
	if resp, found := t.recorded(ctx, callID); found {
		return resp, nil
	}
	resp, err := t.AgentTool.Run(context.WithValue(ctx, forkSubagentMessageKey{}, forkSubagentMessageCall{
		id:             callID,
		organizationID: t.chat.OrganizationID,
	}), call)
	if err != nil || !resp.IsError {
		return resp, err
	}
	// A run of the same call that a takeover left running can record the
	// call after the lookup above. This send then fails on the record's
	// primary key and rolls back, and the message went out once.
	if recordedResp, found := t.recorded(ctx, callID); found {
		return recordedResp, nil
	}
	return resp, nil
}

// recorded looks up the record of the call. found is true when the record
// exists or the lookup failed, and resp is then the tool response.
func (t forkReplaySafeMessageAgentTool) recorded(ctx context.Context, callID uuid.UUID) (resp fantasy.ToolResponse, found bool) {
	fdb, err := forkdb.From(t.server.db)
	if err != nil {
		return fantasy.NewTextErrorResponse(xerrors.Errorf("fork queries: %w", err).Error()), true
	}
	//nolint:gocritic // chatd reads its own bookkeeping rows.
	recipientID, err := fdb.GetChatSubagentMessageRecipient(dbauthz.AsChatd(ctx), forkdb.GetChatSubagentMessageRecipientParams{
		ID:             callID,
		OrganizationID: t.chat.OrganizationID,
		SenderChatID:   t.chat.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fantasy.ToolResponse{}, false
	}
	if err != nil {
		return fantasy.NewTextErrorResponse(xerrors.Errorf("get subagent message: %w", err).Error()), true
	}
	recipient, err := t.server.db.GetChatByID(ctx, recipientID)
	if err != nil {
		return fantasy.NewTextErrorResponse(xerrors.Errorf("get target chat: %w", err).Error()), true
	}
	return toolJSONResponse(subagentChatResult(recipient)), true
}

// forkRecordSubagentMessage is the forkAdmit hook of sendSubagentMessage. For
// a send from forkReplaySafeMessageAgentTool it records the call in the
// send's transaction and drops the sender's records past the retention. Any
// other send records nothing.
func (p *Server) forkRecordSubagentMessage(ctx context.Context, senderID, recipientID uuid.UUID) func(context.Context, database.Store, chatstate.SendMessageResult) error {
	msgCall, ok := ctx.Value(forkSubagentMessageKey{}).(forkSubagentMessageCall)
	if !ok || msgCall.id == uuid.Nil {
		return nil
	}
	return func(ctx context.Context, store database.Store, _ chatstate.SendMessageResult) error {
		txdb, err := forkdb.From(store)
		if err != nil {
			return xerrors.Errorf("fork queries: %w", err)
		}
		now := p.clock.Now()
		//nolint:gocritic // chatd deletes its own bookkeeping rows.
		err = txdb.DeleteChatSubagentMessagesBefore(dbauthz.AsChatd(ctx), forkdb.DeleteChatSubagentMessagesBeforeParams{
			OrganizationID: msgCall.organizationID,
			SenderChatID:   senderID,
			Before:         now.Add(-subagentMessageRetention),
		})
		if err != nil {
			return xerrors.Errorf("delete old subagent messages: %w", err)
		}
		//nolint:gocritic // chatd records its own bookkeeping rows.
		err = txdb.InsertChatSubagentMessage(dbauthz.AsChatd(ctx), forkdb.InsertChatSubagentMessageParams{
			ID:              msgCall.id,
			OrganizationID:  msgCall.organizationID,
			SenderChatID:    senderID,
			RecipientChatID: recipientID,
			CreatedAt:       now,
		})
		if err != nil {
			return xerrors.Errorf("record subagent message: %w", err)
		}
		return nil
	}
}
