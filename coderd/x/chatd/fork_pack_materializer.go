package chatd

// This file is fork code: it writes the plugin packs enabled for a chat into
// the chat's workspace, under managedPackRoot, where the workspace agent
// discovers them (agent/agentcontext/fork_plugins.go).

import (
	"bytes"
	"context"
	"io"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	// packCommitMarker records the pack and commit a pack directory was
	// written at. It is written after every pack file, so a matching marker
	// means the directory is complete.
	packCommitMarker = ".coder-pack-commit"
	// packMaterializeTimeout bounds the work a turn waits for, so an
	// unreachable workspace delays the turn by at most this much.
	packMaterializeTimeout = 10 * time.Second
	// packWriteConcurrency bounds the files of a pack written at once. Each
	// write is one agent round trip, and a pack can hold 2,000 files.
	packWriteConcurrency = 16
)

// forkPackState is the in-memory plugin pack state of a Server. It is lost on
// restart, which only costs a marker read per pack.
type forkPackState struct {
	// written maps materializedPackKey to the marker the agent holds.
	written sync.Map
	// repinned maps repinKey to the marker a chat was last re-pinned for.
	repinned sync.Map
	// paths maps packCommitKey to packPathSet.
	paths sync.Map
}

// materializedPackKey identifies a pack written to a workspace agent. Agent
// IDs change on every build, so a rebuilt workspace is written again.
type materializedPackKey struct {
	agentID uuid.UUID
	packID  uuid.UUID
}

// repinKey identifies a chat re-pinned after a pack reached its agent.
type repinKey struct {
	chatID  uuid.UUID
	agentID uuid.UUID
	packID  uuid.UUID
}

// packMarker is the content of a pack directory's commit marker. It names
// the pack, so a pack reinstalled under the same name at the same commit is
// written again.
func packMarker(pack enabledPack) string {
	return pack.ID.String() + " " + pack.CommitSHA
}

// materializePluginPacks writes each pack enabled for the chat that the
// workspace agent does not hold at the pack's current commit. It is best
// effort: failures are logged and retried on the next turn. The agent reports
// new files on its next context push, which only marks a pinned chat dirty,
// so every chat with the pack enabled is re-pinned on its first dirty turn
// after the pack reached the agent. Files of
// disabled packs and files a pack update removed stay on disk, and
// filterPackResources hides them.
//
// It dials its own connection without the chat headers: the agent records
// paths written by a chat for the chat's git changes, and these writes are
// not the chat's.
func (p *Server) materializePluginPacks(
	ctx context.Context,
	logger slog.Logger,
	chat database.Chat,
	agentID uuid.UUID,
	dial AgentConnFunc,
) {
	if !p.experiments.Enabled(codersdk.ExperimentForkPluginPacks) {
		return
	}
	if agentID == uuid.Nil {
		return
	}
	enabled := p.enabledPacks(ctx, chat)
	p.repinChatAfterPackWrite(ctx, logger, chat, agentID, enabled)
	if dial == nil {
		return
	}
	var pending []enabledPack
	for _, pack := range enabled {
		if marker, ok := p.forkPacks.written.Load(materializedPackKey{agentID, pack.ID}); ok && marker == packMarker(pack) {
			continue
		}
		if slices.ContainsFunc(pending, func(other enabledPack) bool { return other.ID == pack.ID }) {
			continue
		}
		pending = append(pending, pack)
	}
	if len(pending) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, packMaterializeTimeout)
	defer cancel()
	conn, release, err := dial(ctx, agentID)
	if err != nil {
		logger.Warn(ctx, "failed to connect to workspace to write plugin packs", slog.Error(err))
		return
	}
	if release != nil {
		defer release()
	}
	home, err := chattool.ResolveWorkspaceHome(ctx, conn)
	if err != nil {
		logger.Warn(ctx, "failed to resolve workspace home to write plugin packs", slog.Error(err))
		return
	}
	for _, pack := range pending {
		if err := p.materializePack(ctx, conn, home, pack); err != nil {
			logger.Warn(ctx, "failed to write plugin pack to workspace",
				slog.F("pack_id", pack.ID), slog.F("pack_name", pack.Name), slog.Error(err))
			continue
		}
		p.forkPacks.written.Store(materializedPackKey{agentID, pack.ID}, packMarker(pack))
	}
}

// repinChatAfterPackWrite re-pins a dirty chat once for each enabled pack the
// agent holds that the chat has not been re-pinned for: the agent's push of
// the pack's files only marked the chat dirty. Re-pinning also takes in any
// other drift, as the refresh endpoint would.
func (p *Server) repinChatAfterPackWrite(ctx context.Context, logger slog.Logger, chat database.Chat, agentID uuid.UUID, enabled []enabledPack) {
	if !chat.ContextDirtySince.Valid {
		return
	}
	type duePack struct{ marker, name string }
	due := map[repinKey]duePack{}
	for _, pack := range enabled {
		marker := packMarker(pack)
		if held, ok := p.forkPacks.written.Load(materializedPackKey{agentID, pack.ID}); !ok || held != marker {
			continue
		}
		key := repinKey{chat.ID, agentID, pack.ID}
		if last, ok := p.forkPacks.repinned.Load(key); ok && last == marker {
			continue
		}
		due[key] = duePack{marker, pack.Name}
	}
	if len(due) == 0 {
		return
	}
	updated, err := p.RefreshChatContext(ctx, chat)
	if err != nil {
		logger.Warn(ctx, "failed to re-pin chat context after writing plugin packs", slog.Error(err))
		return
	}
	p.publishChatPubsubEvents([]database.Chat{updated}, codersdk.ChatWatchEventKindContextDirty)
	// The chat can be dirty for other drift before the agent pushed the
	// pack, so a pack counts as re-pinned only once its rows are pinned.
	//nolint:gocritic // Chatd reads the pinned rows of a chat it re-pinned.
	pinned, err := p.db.ListChatContextResourcesByChatID(dbauthz.AsChatd(ctx), chat.ID)
	if err != nil {
		logger.Warn(ctx, "failed to read chat context after re-pinning for plugin packs", slog.Error(err))
		return
	}
	pinnedPacks := map[string]bool{}
	for _, row := range pinned {
		if name, ok := managedPackName(row.SourcePath); ok && isPackContribution(row.SourcePath) {
			pinnedPacks[name] = true
		}
	}
	for key, pack := range due {
		if pinnedPacks[pack.name] {
			p.forkPacks.repinned.Store(key, pack.marker)
		}
	}
}

// materializePack writes pack into <home>/.coder/plugins/<name>, unless its
// marker already matches.
func (p *Server) materializePack(ctx context.Context, conn workspacesdk.AgentConn, home string, pack enabledPack) error {
	// Install validates names; checking again keeps a bad row from
	// writing outside managedPackRoot.
	if err := codersdk.NameValid(pack.Name); err != nil {
		return xerrors.Errorf("invalid pack name: %w", err)
	}
	dir := strings.TrimRight(home, `/\`) + managedPackRoot + pack.Name
	marker := dir + "/" + packCommitMarker
	if content, err := readPackMarker(ctx, conn, marker); err == nil && content == packMarker(pack) {
		return nil
	}

	resources, err := p.packResources(ctx, pack.ID)
	if err != nil {
		return err
	}
	for _, resource := range resources {
		if _, ok := packResourcePath(resource.Path); !ok {
			return xerrors.Errorf("pack resource has unsafe path %q", resource.Path)
		}
	}
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(packWriteConcurrency)
	for _, resource := range resources {
		eg.Go(func() error {
			if err := conn.WriteFile(egCtx, dir+"/"+resource.Path, bytes.NewReader(resource.Content)); err != nil {
				return xerrors.Errorf("write %q: %w", resource.Path, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	if err := conn.WriteFile(ctx, marker, strings.NewReader(packMarker(pack))); err != nil {
		return xerrors.Errorf("write commit marker: %w", err)
	}
	return nil
}

// packResources returns the stored files of a pack.
func (p *Server) packResources(ctx context.Context, packID uuid.UUID) ([]forkdb.PluginPackResource, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, xerrors.Errorf("fork queries unavailable: %w", err)
	}
	// Org members cannot read packs, and enabling one for a project or
	// chat is what delivers its files to the chat's workspace.
	//nolint:gocritic // chatd reads the content of a pack enabled for this chat.
	resources, err := fdb.GetPluginPackResources(dbauthz.AsSystemRestricted(ctx), packID)
	if err != nil {
		return nil, xerrors.Errorf("get pack resources: %w", err)
	}
	return resources, nil
}

// readPackMarker returns the commit recorded in a pack directory's marker.
func readPackMarker(ctx context.Context, conn workspacesdk.AgentConn, marker string) (string, error) {
	reader, _, err := conn.ReadFile(ctx, marker, 0, 128)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	commit, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(commit)), nil
}

// packResourcePath returns a stored pack path when it stays inside the pack
// directory and is not the commit marker. Install stores slash-separated
// relative paths, so anything else is rejected rather than rewritten.
func packResourcePath(p string) (string, bool) {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\:`) {
		return "", false
	}
	clean := path.Clean(p)
	if clean != p || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean == packCommitMarker {
		return "", false
	}
	return clean, true
}
