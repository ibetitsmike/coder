package coderd

// This file is fork code: it reports the agent type of child chats, and of
// root chats that started as an agent type, in chat responses when the
// fork-agent-definitions experiment is enabled.

import (
	"context"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
)

// fillForkWatchedChatAgentType sets AgentType on a created chat in a watch
// event. The Agents page adds a created chat from the event alone, so
// without it a new chat shows no type until the next chat list fetch.
func (api *API) fillForkWatchedChatAgentType(ctx context.Context, event *codersdk.ChatWatchEvent) {
	if event.Kind != codersdk.ChatWatchEventKindCreated {
		return
	}
	chats := []codersdk.Chat{event.Chat}
	api.fillForkChatAgentTypes(ctx, chats)
	event.Chat = chats[0]
}

// fillForkChatAgentTypes sets AgentType on every child chat in chats and
// their embedded children, and on each root chat that started as an agent
// type. It is best effort: a failed lookup leaves the field empty.
func (api *API) fillForkChatAgentTypes(ctx context.Context, chats []codersdk.Chat) {
	if !api.Experiments.Enabled(codersdk.ExperimentForkAgentDefinitions) {
		return
	}
	byID := map[uuid.UUID]*codersdk.Chat{}
	var collect func(chats []codersdk.Chat)
	collect = func(chats []codersdk.Chat) {
		for i := range chats {
			byID[chats[i].ID] = &chats[i]
			collect(chats[i].Children)
		}
	}
	collect(chats)
	if len(byID) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		api.Logger.Error(ctx, "fork queries unavailable for chat agent types", slog.Error(err))
		return
	}
	// The caller already read every chat in chats, and the query returns
	// only the agent type of those chats.
	//nolint:gocritic // Batch lookup of chats the caller is authorized to read.
	rows, err := fdb.GetChatSubagentTypesByChatIDs(dbauthz.AsSystemRestricted(ctx), ids)
	if err != nil {
		api.Logger.Error(ctx, "failed to get chat agent types", slog.Error(err))
		return
	}
	for _, row := range rows {
		if chat, ok := byID[row.ChatID]; ok {
			chat.AgentType = row.AgentType
		}
	}
}
