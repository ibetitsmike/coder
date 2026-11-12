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
	agentproto "github.com/coder/coder/v2/agent/proto"
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
// chat filters to its own set. It also drops rows whose defining file or
// manifest is not part of the enabled pack's current commit, since an update
// leaves removed files on disk.
// Packs the user placed outside managedPackRoot are kept. The prompt builder,
// the context resource listing, and the agent type catalog all call it, so
// the gate covers every consumer of the pinned rows. The surviving rows of
// managed packs are then deduplicated by skill and agent name, so a pack that
// is not enabled for the chat cannot hide the same-named resource of one that
// is.
func (p *Server) filterPackResources(ctx context.Context, chat database.Chat, resources []database.ChatContextResource) []database.ChatContextResource {
	if !p.experiments.Enabled(codersdk.ExperimentForkPluginPacks) {
		return slices.DeleteFunc(resources, func(r database.ChatContextResource) bool {
			return isPackContribution(r.SourcePath)
		})
	}
	var enabled map[string]packPathSet
	resources = slices.DeleteFunc(resources, func(r database.ChatContextResource) bool {
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
		files, ok := enabled[name]
		return !ok || !files.defines(r, name)
	})
	return dedupePackResourceNames(resources)
}

// contextResourceNameKey identifies a skill or custom agent by kind and name.
type contextResourceNameKey struct {
	kind database.WorkspaceAgentContextBodyKind
	name string
}

// contextResourceNameKeyOf returns the name key of an OK skill or custom
// agent row. ok is false for any other row, and for a row whose body cannot
// be decoded or has no name; those are never deduplicated.
func contextResourceNameKeyOf(r database.ChatContextResource) (key contextResourceNameKey, ok bool) {
	if r.Status != database.WorkspaceAgentContextResourceStatusOk {
		return contextResourceNameKey{}, false
	}
	var name string
	switch r.BodyKind {
	case database.WorkspaceAgentContextBodyKindSkill:
		body, decoded := decodeSkillMetaBody(r.Body)
		if !decoded {
			return contextResourceNameKey{}, false
		}
		name = body.GetName()
	case database.WorkspaceAgentContextBodyKindSubagent:
		var body agentproto.SubagentBody
		if err := contextBodyUnmarshalOptions.Unmarshal(r.Body, &body); err != nil {
			return contextResourceNameKey{}, false
		}
		name = body.GetName()
	default:
		return contextResourceNameKey{}, false
	}
	return contextResourceNameKey{kind: r.BodyKind, name: name}, name != ""
}

// isManagedPackRow reports whether a row comes from a pack installed under
// managedPackRoot, the packs filterPackResources filters per chat.
func isManagedPackRow(r database.ChatContextResource) bool {
	if !isPackContribution(r.SourcePath) {
		return false
	}
	_, ok := managedPackName(r.SourcePath)
	return ok
}

// dedupePackResourceNames keeps one skill or custom agent row per name among
// the rows of managed packs. The workspace agent leaves same-named rows of
// different managed packs in place because it cannot know which packs a chat
// has enabled, so the choice is made here, among the rows filterPackResources
// kept.
//
// Every other row wins: ordinary rows and the rows of packs outside
// managedPackRoot are never filtered per chat, and the agent has already
// settled their collisions first-wins in scan-root order, which is what lets
// an earlier unmanaged pack beat a managed one. Among managed rows, the first
// in resources order wins. That is source order, so the pack whose directory
// name sorts first, the same order the agent scans in. The order is
// deterministic but arbitrary: pack priority is not persisted, so a chat that
// enables two packs with the same name sees the first.
func dedupePackResourceNames(resources []database.ChatContextResource) []database.ChatContextResource {
	if !slices.ContainsFunc(resources, isManagedPackRow) {
		return resources
	}
	seen := map[contextResourceNameKey]struct{}{}
	for _, r := range resources {
		if isManagedPackRow(r) {
			continue
		}
		if key, ok := contextResourceNameKeyOf(r); ok {
			seen[key] = struct{}{}
		}
	}
	return slices.DeleteFunc(resources, func(r database.ChatContextResource) bool {
		if !isManagedPackRow(r) {
			return false
		}
		key, ok := contextResourceNameKeyOf(r)
		if !ok {
			return false
		}
		if _, dup := seen[key]; dup {
			return true
		}
		seen[key] = struct{}{}
		return false
	})
}

// packPathSet holds the paths of a pack's files, relative to the pack
// directory.
type packPathSet map[string]bool

// defines reports whether a row of the installed pack name is backed by the
// pack's files: its manifest, and its source file or, for a skill directory,
// the directory's SKILL.md.
func (files packPathSet) defines(r database.ChatContextResource, name string) bool {
	if !files[packRelativePath(r.SourcePath, name)] {
		return false
	}
	source := packRelativePath(r.Source, name)
	return source != "" && (files[source] || files[path.Join(source, "SKILL.md")])
}

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
	if cached, ok := p.forkPacks.paths.Load(key); ok {
		if paths, ok := cached.(packPathSet); ok {
			return paths
		}
	}
	resources, err := p.packResources(ctx, pack.ID)
	if err != nil {
		p.logger.Warn(ctx, "failed to read plugin pack files", slog.F("pack_id", pack.ID), slog.Error(err))
		return packPathSet{}
	}
	paths := packPathSet{}
	for _, resource := range resources {
		paths[resource.Path] = true
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
