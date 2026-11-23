package chatd

// This file is fork code: with the fork-family-messages experiment on,
// wait_agent returns early when a family message waits in the steer lane of
// the waiting chat. The tool call then ends, and the message reaches the
// model at the next model call instead of after the wait. appendForkTools
// wraps the upstream tool, so fork features never edit the wait_agent tool
// itself.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/db2sdk"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/codersdk"
)

const familyMessageWaitNote = "A message from another chat in your family arrived while you waited. " +
	"It is delivered right after this result. The wait ended before the child finished; " +
	"call wait_agent again to keep waiting for it."

// familyMessageWaitRerunTimeout bounds the second wait for a child that
// finished while the first wait ended for a family message. That wait
// returns at once unless the child runs again.
const familyMessageWaitRerunTimeout = 5 * time.Second

var errFamilyMessagePending = xerrors.New("a family message is pending")

// familyMessageWaitResult keeps its fields in sorted JSON key order, like the
// subagent tool results.
type familyMessageWaitResult struct {
	ChatID               string `json:"chat_id"`
	FamilyMessagePending bool   `json:"family_message_pending"`
	Note                 string `json:"note"`
	Status               string `json:"status"`
	Title                string `json:"title"`
}

// familyMessageWaitTool is wait_agent that stops waiting when a family
// message is pending for the waiting chat.
type familyMessageWaitTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withFamilyMessageWait wraps the wait_agent tool in tools, if there is one,
// in familyMessageWaitTool.
func (p *Server) withFamilyMessageWait(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	for i, tool := range tools {
		if tool.Info().Name == waitAgentToolName {
			tools[i] = familyMessageWaitTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

func (t familyMessageWaitTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description += " family_message_pending true means a message from another chat in your family " +
		"ended the wait before the child finished; the message follows this result."
	return info
}

func (t familyMessageWaitTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var args waitAgentArgs
	if json.Unmarshal([]byte(call.Input), &args) != nil {
		return t.AgentTool.Run(ctx, call)
	}
	childID, err := parseSubagentToolChatID(args.ChatID)
	if err != nil {
		return t.AgentTool.Run(ctx, call)
	}
	// The upstream tool reports a chat this chat may not wait on, so the
	// early result never describes such a chat.
	if ok, err := t.server.isSubagentTarget(ctx, t.chat.ID, childID); err != nil || !ok {
		return t.AgentTool.Run(ctx, call)
	}

	waitCtx, cancel := context.WithCancelCause(ctx)
	stopped := t.server.watchFamilyMessages(waitCtx, t.chat.ID, func() { cancel(errFamilyMessagePending) })
	resp, err := t.AgentTool.Run(waitCtx, call)
	endedEarly := endedForFamilyMessage(ctx, waitCtx, resp, err)
	cancel(nil)
	<-stopped
	// A wait that completed while the message arrived keeps its result; the
	// message still follows it.
	if !endedEarly {
		return resp, err
	}
	child, err := t.server.db.GetChatByID(ctx, childID)
	if err != nil {
		return fantasy.NewTextErrorResponse("read the child chat: " + err.Error()), nil
	}
	// The cancel can reach a wrapper that still reads more after the child
	// finished, such as a pending question or a diff summary. The child's
	// result is ready then, so the wait runs again and returns it.
	if subagentFinished(child.Status) {
		rerunCtx, cancelRerun := context.WithTimeoutCause(ctx, familyMessageWaitRerunTimeout, errFamilyMessagePending)
		resp, err := t.AgentTool.Run(rerunCtx, call)
		endedEarly := endedForFamilyMessage(ctx, rerunCtx, resp, err)
		cancelRerun()
		if !endedEarly {
			return resp, err
		}
		if child, err = t.server.db.GetChatByID(ctx, childID); err != nil {
			return fantasy.NewTextErrorResponse("read the child chat: " + err.Error()), nil
		}
	}
	return toolJSONResponse(familyMessageWaitResult{
		ChatID:               child.ID.String(),
		FamilyMessagePending: true,
		Note:                 familyMessageWaitNote,
		Status:               string(child.Status),
		Title:                child.Title,
	}), nil
}

// endedForFamilyMessage reports whether a wait under waitCtx ended without a
// result because errFamilyMessagePending canceled it.
func endedForFamilyMessage(ctx, waitCtx context.Context, resp fantasy.ToolResponse, err error) bool {
	return ctx.Err() == nil && xerrors.Is(context.Cause(waitCtx), errFamilyMessagePending) && (err != nil || resp.IsError)
}

// subagentFinished reports whether a wait for a child in status returns at
// once, as checkSubagentCompletion decides.
func subagentFinished(status database.ChatStatus) bool {
	return status != database.ChatStatusRunning &&
		status != database.ChatStatusInterrupting &&
		status != database.ChatStatusPaused
}

// watchFamilyMessages calls found once when chatID has a family message that
// the next delivery point moves into history, and stops when ctx ends. The
// returned channel closes when the watch stops. Every queued send commits
// through the chat's state machine, which publishes on the chat's state
// channel, so the watch reads the queue again on each notification. The
// fallback tick covers a lost notification.
func (p *Server) watchFamilyMessages(ctx context.Context, chatID uuid.UUID, found func()) <-chan struct{} {
	stopped := make(chan struct{})
	// Subscribe before the first read, so no message lands unseen between
	// the read and the subscription.
	pollInterval := subagentAwaitFallbackPoll
	ch := make(chan struct{}, 1)
	notifyCh := (<-chan struct{})(ch)
	unsubscribe, subErr := p.pubsub.SubscribeWithErr(
		coderdpubsub.ChatStateUpdateChannel(chatID),
		func(context.Context, []byte, error) {
			select {
			case ch <- struct{}{}:
			default:
			}
		},
	)
	if subErr != nil {
		pollInterval = subagentAwaitPollInterval
		notifyCh = nil
	}
	go func() {
		defer close(stopped)
		if subErr == nil {
			defer unsubscribe()
		}
		ticker := p.clock.NewTicker(pollInterval, "chatd", "family_message_wait_poll")
		defer ticker.Stop()
		for {
			pending, err := p.pendingFamilyMessage(ctx, chatID)
			if err != nil && ctx.Err() == nil {
				p.logger.Warn(ctx, "read queued family messages", slog.F("chat_id", chatID), slog.Error(err))
			}
			if pending {
				found()
				return
			}
			select {
			case <-notifyCh:
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	return stopped
}

// pendingFamilyMessage reports whether the steer lane of chatID holds a
// family message that the next delivery point delivers. A steer message
// under edit holds the whole lane, so a lane with one delivers nothing.
func (p *Server) pendingFamilyMessage(ctx context.Context, chatID uuid.UUID) (bool, error) {
	queued, err := p.db.GetChatQueuedMessages(ctx, chatID)
	if err != nil {
		return false, xerrors.Errorf("get queued messages: %w", err)
	}
	pending := false
	for _, row := range queued {
		if row.BusyBehavior != database.ChatBusyBehaviorSteer {
			break
		}
		if row.EditingSince.Valid {
			return false, nil
		}
		pending = pending || isFamilyMessage(db2sdk.ChatQueuedMessage(row).Content)
	}
	return pending, nil
}

// isFamilyMessage reports whether content is a message that
// message_family_chat sent.
func isFamilyMessage(content []codersdk.ChatMessagePart) bool {
	for _, part := range content {
		if part.Type == codersdk.ChatMessagePartTypeText && strings.HasPrefix(part.Text, familyMessageEnvelopePrefix) {
			return true
		}
	}
	return false
}
