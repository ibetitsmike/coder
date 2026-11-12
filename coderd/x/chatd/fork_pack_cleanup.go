package chatd

// This file is fork code: it empties the commit marker of each managed plugin
// pack no chat on a workspace enables, so the workspace agent stops the
// pack's MCP servers (agent/agentcontext/fork_pack_mcp.go).

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// heldPack is a pack this server wrote to an agent, and its directory name.
type heldPack struct {
	key  materializedPackKey
	name string
}

// packCleanup is the marker cleanup a materialize pass does once connected.
type packCleanup struct {
	// disabled holds the packs this server wrote to the agent that no chat
	// on the workspace enables.
	disabled []heldPack
	// sweep also clears the unenabled packs on disk this server does not
	// know about, such as packs written before it started. It runs once per
	// agent, on a pass that connects anyway.
	sweep bool
	// enabledNames are the pack directories a chat on the workspace
	// enables, which are never cleared.
	enabledNames map[string]bool
}

// planPackCleanup decides which pack markers to empty for the agent. It
// reads the other chats on the workspace only when this server holds a pack
// for the agent that the chat does not enable, or when the pass writes
// pending packs and the agent was not swept yet. Only a marker to clear adds
// a connection.
func (p *Server) planPackCleanup(ctx context.Context, logger slog.Logger, chat database.Chat, agentID uuid.UUID, enabled, pending []enabledPack) packCleanup {
	if !chat.WorkspaceID.Valid {
		return packCleanup{}
	}
	chatEnabled := make(map[uuid.UUID]bool, len(enabled))
	for _, pack := range enabled {
		chatEnabled[pack.ID] = true
	}
	var stale []heldPack
	p.forkPacks.written.Range(func(key, value any) bool {
		k, ok := key.(materializedPackKey)
		if !ok || k.agentID != agentID || chatEnabled[k.packID] {
			return true
		}
		if held, ok := value.(writtenPack); ok {
			stale = append(stale, heldPack{key: k, name: held.name})
		}
		return true
	})
	_, swept := p.forkPacks.swept.Load(agentID)
	if len(stale) == 0 && (swept || len(pending) == 0) {
		return packCleanup{}
	}

	ids, names, ok := p.workspaceEnabledPacks(ctx, logger, chat, enabled)
	if !ok {
		return packCleanup{}
	}
	plan := packCleanup{enabledNames: names}
	for _, s := range stale {
		if ids[s.key.packID] {
			continue
		}
		if names[s.name] {
			// An enabled pack of the same name owns the directory.
			p.forgetWrittenPack(s.key)
			continue
		}
		plan.disabled = append(plan.disabled, s)
	}
	plan.sweep = !swept && (len(pending) > 0 || len(plan.disabled) > 0)
	return plan
}

// workspaceEnabledPacks returns the IDs and names of the packs enabled for
// the chat or any other unarchived chat on its workspace.
func (p *Server) workspaceEnabledPacks(ctx context.Context, logger slog.Logger, chat database.Chat, enabled []enabledPack) (map[uuid.UUID]bool, map[string]bool, bool) {
	//nolint:gocritic // Chatd reads which chats share the workspace to keep their packs.
	chats, err := p.db.GetChatsByWorkspaceIDs(dbauthz.AsChatd(ctx), []uuid.UUID{chat.WorkspaceID.UUID})
	if err != nil {
		logger.Warn(ctx, "failed to list workspace chats to clean up plugin packs", slog.Error(err))
		return nil, nil, false
	}
	ids, names := map[uuid.UUID]bool{}, map[string]bool{}
	add := func(packs []enabledPack) {
		for _, pack := range packs {
			ids[pack.ID] = true
			names[pack.Name] = true
		}
	}
	add(enabled)
	for _, other := range chats {
		if other.ID != chat.ID {
			add(p.enabledPacks(ctx, other))
		}
	}
	return ids, names, true
}

// clearDisabledPacks empties the markers plan names. AgentConn cannot delete
// files, and the agent starts no MCP server of a managed pack without a
// marker. A cleared pack is forgotten, so enabling it again rewrites it.
func (p *Server) clearDisabledPacks(ctx context.Context, logger slog.Logger, conn workspacesdk.AgentConn, home string, agentID uuid.UUID, plan packCleanup) {
	root := strings.TrimRight(home, `/\`) + managedPackRoot
	for _, stale := range plan.disabled {
		if err := conn.WriteFile(ctx, root+stale.name+"/"+packCommitMarker, strings.NewReader("")); err != nil {
			logger.Warn(ctx, "failed to clear disabled plugin pack marker",
				slog.F("pack_id", stale.key.packID), slog.F("pack_name", stale.name), slog.Error(err))
			continue
		}
		p.forgetWrittenPack(stale.key)
	}
	if !plan.sweep {
		return
	}
	listing, err := conn.LS(ctx, "", workspacesdk.LSRequest{
		Path:       []string{".coder", "plugins"},
		Relativity: workspacesdk.LSRelativityHome,
	})
	if err != nil {
		logger.Debug(ctx, "failed to list plugin packs to clear disabled ones", slog.Error(err))
		return
	}
	for _, entry := range listing.Contents {
		if !entry.IsDir || plan.enabledNames[entry.Name] || codersdk.NameValid(entry.Name) != nil {
			continue
		}
		marker := root + entry.Name + "/" + packCommitMarker
		if content, err := readPackMarker(ctx, conn, marker); err != nil || content == "" {
			continue
		}
		if err := conn.WriteFile(ctx, marker, strings.NewReader("")); err != nil {
			logger.Warn(ctx, "failed to clear disabled plugin pack marker", slog.F("pack_name", entry.Name), slog.Error(err))
			return
		}
	}
	p.forkPacks.swept.Store(agentID, struct{}{})
}

// forgetWrittenPack drops a held pack and the chats re-pinned for it, so the
// next pass that enables it rewrites it and re-pins them. It also drops an
// entry a concurrent pass stored meanwhile, whose marker the clear may have
// emptied; that costs at most one rewrite.
func (p *Server) forgetWrittenPack(key materializedPackKey) {
	p.forkPacks.written.Delete(key)
	p.forkPacks.repinned.Range(func(k, _ any) bool {
		if r, ok := k.(repinKey); ok && r.agentID == key.agentID && r.packID == key.packID {
			p.forkPacks.repinned.Delete(k)
		}
		return true
	})
}
