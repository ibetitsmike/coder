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
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

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
	source := packRelativePath(packRowSourceFile(r), name)
	return source != "" && (files[source] || files[path.Join(source, "SKILL.md")])
}

// packMCPServerSep joins a pack MCP server's declaring file and name in its
// row source. Keep in sync with agent/agentcontext/fork_pack_mcp.go.
const packMCPServerSep = "#"

// packRowSourceFile returns the file a pack row's source names. An MCP
// server's source is its declaring file (a config file, or the manifest for
// inline servers) and its name, because one file declares several servers
// and a source is unique per agent. Splitting at the last separator fails
// closed for a server name that holds one.
func packRowSourceFile(r database.ChatContextResource) string {
	if r.BodyKind != database.WorkspaceAgentContextBodyKindMcpServer {
		return r.Source
	}
	if i := strings.LastIndex(r.Source, packMCPServerSep); i >= 0 {
		return r.Source[:i]
	}
	return r.Source
}

// packPathsIdle is how long the cached paths of a pack commit stay after
// their last read. A step reads the paths of its pinned commit each time it
// filters pack rows, so a step pinned to an older commit keeps them while
// newer steps read newer commits.
const packPathsIdle = time.Hour

// packCommitPaths is the file paths of a pack at one commit and when they
// were last read.
type packCommitPaths struct {
	paths    packPathSet
	lastRead time.Time
}

// packPathsEntry is the cached paths of one pack, keyed by commit. Only the
// commits read in the last packPathsIdle stay.
type packPathsEntry struct {
	mu      sync.Mutex
	commits map[string]*packCommitPaths
}

// packPaths returns the paths of the pack's files at its current commit. A
// commit's files never change, so they are read once per commit. The cache
// holds one entry per pack, with the commits read in the last packPathsIdle.
// A failed read, or a read of a commit the pack moved past, is logged and
// returns no paths, so the gate fails closed.
func (p *Server) packPaths(ctx context.Context, pack enabledPack) packPathSet {
	cached, _ := p.forkPacks.paths.LoadOrStore(pack.ID, &packPathsEntry{})
	entry, ok := cached.(*packPathsEntry)
	if !ok {
		return packPathSet{}
	}
	now := p.clock.Now()
	entry.mu.Lock()
	if c, ok := entry.commits[pack.CommitSHA]; ok {
		c.lastRead = now
		entry.mu.Unlock()
		return c.paths
	}
	entry.mu.Unlock()

	resources, err := p.packResources(ctx, pack.ID)
	if err != nil {
		p.logger.Warn(ctx, "failed to read plugin pack files", slog.F("pack_id", pack.ID), slog.Error(err))
		return packPathSet{}
	}
	// The resource table holds only the files of the pack's current commit,
	// and an update replaces them and the commit in one transaction. Read the
	// commit after the files: while it is still the pinned one, the files are
	// that commit's. Once the pack moved on, the pinned commit's files are
	// gone, so the gate fails closed for this step and caches nothing.
	current, err := p.packCommit(ctx, pack.ID)
	if err != nil {
		p.logger.Warn(ctx, "failed to read plugin pack commit", slog.F("pack_id", pack.ID), slog.Error(err))
		return packPathSet{}
	}
	if current != pack.CommitSHA {
		p.logger.Info(ctx, "plugin pack moved past the commit of the step",
			slog.F("pack_id", pack.ID), slog.F("step_commit", pack.CommitSHA), slog.F("current_commit", current))
		return packPathSet{}
	}
	paths := packPathSet{}
	for _, resource := range resources {
		paths[resource.Path] = true
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.commits == nil {
		entry.commits = map[string]*packCommitPaths{}
	}
	for commit, c := range entry.commits {
		if now.Sub(c.lastRead) > packPathsIdle {
			delete(entry.commits, commit)
		}
	}
	if c, ok := entry.commits[pack.CommitSHA]; ok {
		// A concurrent read stored the commit first.
		c.lastRead = now
		return c.paths
	}
	entry.commits[pack.CommitSHA] = &packCommitPaths{paths: paths, lastRead: now}
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
// also see the packs of the chats that spawned them. A pack that the chat or
// an ancestor turned off is left out, even when another place enables it. A
// pack enabled in more than one place is listed more than once. A failed
// lookup is logged and enables nothing, so the gate fails closed. A
// generation step resolves the packs of its chat once.
func (p *Server) enabledPacks(ctx context.Context, chat database.Chat) []enabledPack {
	return stepEnabledPacks(ctx, chat, func() ([]enabledPack, bool) {
		return p.loadEnabledPacks(ctx, chat)
	})
}

// loadEnabledPacks reads the packs that enabledPacks returns. complete is
// false when a lookup failed, including the lookups of the chat's project
// and ancestors.
func (p *Server) loadEnabledPacks(ctx context.Context, chat database.Chat) (enabled []enabledPack, complete bool) {
	complete = true
	logger := p.logger.With(slog.F("chat_id", chat.ID))
	fdb, err := forkdb.From(p.db)
	if err != nil {
		logger.Warn(ctx, "fork queries unavailable for plugin pack enablement", slog.Error(err))
		return enabled, false
	}
	// Like project skills, enablement and the usable-project check run as
	// chatd and answer for the chat owner, so the gate does not depend on
	// the caller.
	//nolint:gocritic // Pack enablement applies to the chat owner, not the caller.
	chatdCtx := dbauthz.AsChatd(ctx)
	projectID, err := p.packProjectID(chatdCtx, chat)
	if err != nil {
		logger.Warn(ctx, "failed to resolve chat project for plugin packs", slog.Error(err))
		complete = false
	}
	if projectID.Valid {
		rows, err := fdb.GetPluginPacksByProjectID(chatdCtx, projectID.UUID)
		if err != nil {
			logger.Warn(ctx, "failed to list project plugin packs", slog.F("project_id", projectID.UUID), slog.Error(err))
			complete = false
		}
		for _, row := range rows {
			enabled = append(enabled, enabledPack{ID: row.ID, Name: row.Name, CommitSHA: row.CommitSha})
		}
	}
	// A subagent inherits the packs of every ancestor: its root and, with
	// nested subagents, the chats in between.
	chatIDs := []uuid.UUID{chat.ID}
	ancestorIDs, err := walkAncestorChatIDs(chatdCtx, p.db, p.nestedSubagentsEnabled(), chat)
	if err != nil {
		logger.Warn(ctx, "resolve ancestor chat", slog.Error(err))
		complete = false
	}
	for _, id := range ancestorIDs {
		if ancestorID, err := uuid.Parse(id); err == nil {
			chatIDs = append(chatIDs, ancestorID)
		}
	}
	if chat.RootChatID.Valid && !slices.Contains(chatIDs, chat.RootChatID.UUID) {
		chatIDs = append(chatIDs, chat.RootChatID.UUID)
	}
	excluded := make(map[uuid.UUID]bool)
	for _, chatID := range chatIDs {
		rows, err := fdb.GetPluginPacksByChatID(chatdCtx, chatID)
		// The agent type catalog passes a chat that does not exist yet.
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			logger.Warn(ctx, "failed to list chat plugin packs", slog.F("pack_chat_id", chatID), slog.Error(err))
			complete = false
		}
		for _, row := range rows {
			enabled = append(enabled, enabledPack{ID: row.ID, Name: row.Name, CommitSHA: row.CommitSha})
		}
		excludedRows, err := fdb.GetExcludedPluginPacksByChatID(chatdCtx, chatID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			logger.Warn(ctx, "failed to list excluded chat plugin packs", slog.F("pack_chat_id", chatID), slog.Error(err))
			return nil, false
		}
		for _, row := range excludedRows {
			excluded[row.ID] = true
		}
	}
	return slices.DeleteFunc(enabled, func(pack enabledPack) bool { return excluded[pack.ID] }), complete
}

// packProjectID returns the project that resolveUsableChatProjectID returns,
// the chat's or its root's when the chat owner can use it, and returns a
// failed lookup instead of logging it, so pack enablement does not keep packs
// read without the project. A missing root chat or project is no project, as
// there.
func (p *Server) packProjectID(ctx context.Context, chat database.Chat) (uuid.NullUUID, error) {
	projectID := chat.ProjectID
	if !projectID.Valid && chat.RootChatID.Valid && chat.RootChatID.UUID != chat.ID {
		root, err := p.db.GetChatByID(ctx, chat.RootChatID.UUID)
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.NullUUID{}, nil
		}
		if err != nil {
			return uuid.NullUUID{}, xerrors.Errorf("get root chat %s: %w", chat.RootChatID.UUID, err)
		}
		projectID = root.ProjectID
	}
	if !projectID.Valid {
		return uuid.NullUUID{}, nil
	}
	project, err := p.db.GetChatProjectByID(ctx, projectID.UUID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.NullUUID{}, nil
	}
	if err != nil {
		return uuid.NullUUID{}, xerrors.Errorf("get chat project %s: %w", projectID.UUID, err)
	}
	usable, err := ChatProjectUsableBy(ctx, p.db, project, chat.OwnerID)
	if err != nil {
		return uuid.NullUUID{}, xerrors.Errorf("check access to chat project %s: %w", projectID.UUID, err)
	}
	if !usable {
		return uuid.NullUUID{}, nil
	}
	return projectID, nil
}
