package chatd

// This file is fork code: it publishes fork_change watch events, which tell
// the owner's clients to refetch a fork panel of a chat instead of polling
// it.

import (
	"context"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// forkFamilyRoot returns the root of chat's family. A root chat stores no
// root_chat_id.
func forkFamilyRoot(chat database.Chat) uuid.UUID {
	switch {
	case chat.RootChatID.Valid:
		return chat.RootChatID.UUID
	case chat.ParentChatID.Valid:
		return chat.ParentChatID.UUID
	default:
		return chat.ID
	}
}

// publishForkChange tells the watch stream of chat's owner that facet of
// chat changed. Call it after the change committed. The watch stream of a
// user carries only that user's chats, so other readers of the chat see the
// change at their next resync.
func (p *Server) publishForkChange(chat database.Chat, facet codersdk.ChatWatchForkFacet, runID *uuid.UUID) {
	p.publishChatEvent(chat.OwnerID, chat.ID, codersdk.ChatWatchEvent{
		Kind: codersdk.ChatWatchEventKindForkChange,
		ForkChange: &codersdk.ChatWatchForkChange{
			ChatID:        chat.ID,
			RootChatID:    forkFamilyRoot(chat),
			Facets:        []codersdk.ChatWatchForkFacet{facet},
			WorkflowRunID: runID,
		},
	})
}

// publishForkLoopScheduled reports the wake-up that a /loop message
// created, after the message committed. It does nothing for a nil loop.
func (p *Server) publishForkLoopScheduled(chat database.Chat, loop *forkLoop) {
	if loop != nil {
		p.publishForkChange(chat, codersdk.ChatWatchForkFacetWakeups, nil)
	}
}

// changed reports a committed journal write of run runID to the owner of
// the run's chat. A run of this process names its chat; any other run costs
// two reads, which only the rare writes outside a live run pay: a stop or a
// report of an interrupted run, and a resume before it registers the run.
func (j workflowJournal) changed(ctx context.Context, runID uuid.UUID, facet codersdk.ChatWatchForkFacet) {
	if run := j.p.workflows.get(runID); run != nil {
		j.p.publishForkChange(run.parent, facet, &runID)
		return
	}
	row, found, err := j.getRun(ctx, runID)
	if err != nil || !found {
		if err != nil {
			j.warn(ctx, "read workflow run for its watch event", runID, err)
		}
		return
	}
	chat, err := j.p.db.GetChatByID(journalCtx(ctx), row.ChatID)
	if err != nil {
		j.p.logger.Warn(ctx, "read the chat of a workflow run for its watch event",
			slog.F("workflow_id", runID), slog.Error(err))
		return
	}
	j.p.publishForkChange(chat, facet, &runID)
}
