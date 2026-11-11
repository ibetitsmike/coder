package chatd

// This file is fork code: with the fork-plugin-packs experiment off, resources
// contributed by a plugin pack must not reach the prompt or the context
// popup, so a deployment with the flag off matches upstream, whose resolver
// ignores plugin manifests. The agentcontext resolver stamps every
// pack-contributed resource with the pack's manifest path in source_path
// (agent/agentcontext/fork_plugins.go); this gate matches on that marker's
// base name. A user-added source path ending in plugin.json would be
// dropped too, which no current source type produces.

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
)

// packContributionMarker is the base name of every plugin manifest the
// resolver stamps pack contributions with. Keep in sync with the manifest
// candidates in agent/agentcontext/fork_plugins.go.
const packContributionMarker = "plugin.json"

// isPackContribution reports whether a chat context resource row was
// contributed by a plugin pack. It splits on both separators: the source
// path is stamped by the workspace agent, which may run on Windows while
// coderd runs on Linux.
func isPackContribution(sourcePath string) bool {
	base := strings.TrimSpace(sourcePath)
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return base == packContributionMarker
}

// managedPackRoot is the workspace directory, relative to the home
// directory, that coderd writes installed packs into, one subdirectory per
// pack name. Keep in sync with defaultBuiltinRoots in
// agent/agentcontext/defaults.go.
const managedPackRoot = "/.coder/plugins/"

// managedPackName returns the name of the installed pack a pack-contributed
// row comes from, or false when the pack lives outside managedPackRoot. The
// agent only treats a scan root and its immediate children as packs, so the
// directory after managedPackRoot is the pack's directory.
func managedPackName(sourcePath string) (string, bool) {
	normalized := strings.ReplaceAll(sourcePath, `\`, "/")
	i := strings.LastIndex(normalized, managedPackRoot)
	if i < 0 {
		return "", false
	}
	name, _, ok := strings.Cut(normalized[i+len(managedPackRoot):], "/")
	return name, ok && name != ""
}

// filterPackResources drops pack-contributed resources when fork-plugin-packs is
// off. With it on, it drops the rows of installed packs that are not enabled
// for the chat: every workspace receives the packs of all its chats, so each
// chat filters to its own set. It also drops rows whose file is no longer
// part of the enabled pack's current commit, since an update leaves removed
// files on disk.
// Packs the user placed outside managedPackRoot are kept. The prompt builder,
// the context resource listing, and the agent type catalog all call it, so
// the gate covers every consumer of the pinned rows.
func (p *Server) filterPackResources(ctx context.Context, chat database.Chat, resources []database.ChatContextResource) []database.ChatContextResource {
	if !p.experiments.Enabled(codersdk.ExperimentForkPluginPacks) {
		return slices.DeleteFunc(resources, func(r database.ChatContextResource) bool {
			return isPackContribution(r.SourcePath)
		})
	}
	var enabled map[string]packPathSet
	return slices.DeleteFunc(resources, func(r database.ChatContextResource) bool {
		if !isPackContribution(r.SourcePath) {
			return false
		}
		name, ok := managedPackName(r.SourcePath)
		if !ok {
			return false
		}
		if enabled == nil {
			enabled = map[string]packPathSet{}
			for _, pack := range p.enabledPacks(ctx, chat) {
				if _, ok := enabled[pack.Name]; !ok {
					enabled[pack.Name] = p.packPaths(ctx, pack)
				}
			}
		}
		paths, ok := enabled[name]
		return !ok || !paths[packRelativePath(r.Source, name)]
	})
}

// packPathSet holds the paths of a pack's files and of the directories that
// contain them, relative to the pack directory.
type packPathSet map[string]bool

// packCommitKey identifies the files of a pack at one commit.
type packCommitKey struct {
	packID uuid.UUID
	commit string
}

// packPaths returns the paths of the pack's files at its current commit. A
// commit's files never change, so they are read once per commit. A failed
// read is logged and returns no paths, so the gate fails closed.
func (p *Server) packPaths(ctx context.Context, pack enabledPack) packPathSet {
	key := packCommitKey{pack.ID, pack.CommitSHA}
	if paths, ok := p.forkPacks.paths.Load(key); ok {
		return paths.(packPathSet)
	}
	resources, err := p.packResources(ctx, pack.ID)
	if err != nil {
		p.logger.Warn(ctx, "failed to read plugin pack files", slog.F("pack_id", pack.ID), slog.Error(err))
		return packPathSet{}
	}
	paths := packPathSet{}
	for _, resource := range resources {
		for rel := resource.Path; rel != "." && rel != "/" && rel != ""; rel = path.Dir(rel) {
			paths[rel] = true
		}
	}
	p.forkPacks.paths.Store(key, paths)
	return paths
}

// packRelativePath returns source relative to the directory of the installed
// pack name, or "" when source is outside it.
func packRelativePath(source, name string) string {
	normalized := strings.ReplaceAll(source, `\`, "/")
	dir := managedPackRoot + name + "/"
	i := strings.LastIndex(normalized, dir)
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(normalized[i+len(dir):], "/")
}

// enabledPack is a plugin pack enabled for a chat.
type enabledPack struct {
	ID        uuid.UUID
	Name      string
	CommitSHA string
}

// enabledPacks returns the packs enabled for the chat's project, when the
// chat owner can use it, for the chat, and for its ancestors, so subagents
// also see the packs of the chats that spawned them. A pack enabled in more
// than one place is listed more than once. A failed lookup is logged and
// enables nothing, so the gate fails closed.
func (p *Server) enabledPacks(ctx context.Context, chat database.Chat) []enabledPack {
	var enabled []enabledPack
	logger := p.logger.With(slog.F("chat_id", chat.ID))
	fdb, err := forkdb.From(p.db)
	if err != nil {
		logger.Warn(ctx, "fork queries unavailable for plugin pack enablement", slog.Error(err))
		return enabled
	}
	// Like project skills, enablement and the usable-project check run as
	// chatd and answer for the chat owner, so the gate does not depend on
	// the caller.
	//nolint:gocritic // Pack enablement applies to the chat owner, not the caller.
	chatdCtx := dbauthz.AsChatd(ctx)
	if projectID := p.resolveUsableChatProjectID(chatdCtx, logger, chat); projectID.Valid {
		rows, err := fdb.GetPluginPacksByProjectID(chatdCtx, projectID.UUID)
		if err != nil {
			logger.Warn(ctx, "failed to list project plugin packs", slog.F("project_id", projectID.UUID), slog.Error(err))
		}
		for _, row := range rows {
			enabled = append(enabled, enabledPack{ID: row.ID, Name: row.Name, CommitSHA: row.CommitSha})
		}
	}
	// A subagent inherits the packs of every ancestor: its root and, with
	// nested subagents, the chats in between.
	chatIDs := []uuid.UUID{chat.ID}
	for _, id := range AncestorChatIDs(chatdCtx, p.db, logger, p.nestedSubagentsEnabled(), chat) {
		if ancestorID, err := uuid.Parse(id); err == nil {
			chatIDs = append(chatIDs, ancestorID)
		}
	}
	if chat.RootChatID.Valid && !slices.Contains(chatIDs, chat.RootChatID.UUID) {
		chatIDs = append(chatIDs, chat.RootChatID.UUID)
	}
	for _, chatID := range chatIDs {
		rows, err := fdb.GetPluginPacksByChatID(chatdCtx, chatID)
		// The agent type catalog passes a chat that does not exist yet.
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			logger.Warn(ctx, "failed to list chat plugin packs", slog.F("pack_chat_id", chatID), slog.Error(err))
		}
		for _, row := range rows {
			enabled = append(enabled, enabledPack{ID: row.ID, Name: row.Name, CommitSHA: row.CommitSha})
		}
	}
	return enabled
}
