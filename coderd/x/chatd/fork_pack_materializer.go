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
	"time"

	"github.com/google/uuid"
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
	// packCommitMarker holds the commit a pack directory was written at. It
	// is written after every pack file, so a matching marker means the
	// directory is complete.
	packCommitMarker = ".coder-pack-commit"
	// packMaterializeTimeout bounds the work a turn waits for, so an
	// unreachable workspace delays the turn by at most this much.
	packMaterializeTimeout = 10 * time.Second
)

// materializedPackKey identifies a pack written to a workspace agent. Agent
// IDs change on every build, so a rebuilt workspace is written again.
type materializedPackKey struct {
	agentID uuid.UUID
	packID  uuid.UUID
}

// materializePluginPacks writes each pack enabled for the chat that the
// workspace agent does not hold at the pack's current commit. It is best
// effort: failures are logged and retried on the next turn. The agent reports
// new files on its next context push, so a pack written during a turn reaches
// the model from a later turn. Files of disabled packs and files a pack
// update removed stay on disk, and filterPackResources hides them.
func (p *Server) materializePluginPacks(
	ctx context.Context,
	logger slog.Logger,
	chat database.Chat,
	agentID uuid.UUID,
	getConn func(context.Context) (workspacesdk.AgentConn, error),
) {
	if !p.experiments.Enabled(codersdk.ExperimentForkPluginPacks) || agentID == uuid.Nil {
		return
	}
	var pending []enabledPack
	for _, pack := range p.enabledPacks(ctx, chat) {
		if commit, ok := p.materializedPacks.Load(materializedPackKey{agentID, pack.ID}); ok && commit == pack.CommitSHA {
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
	conn, err := getConn(ctx)
	if err != nil {
		logger.Warn(ctx, "failed to connect to workspace to write plugin packs", slog.Error(err))
		return
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
		p.materializedPacks.Store(materializedPackKey{agentID, pack.ID}, pack.CommitSHA)
	}
}

// materializePack writes pack into <home>/.coder/plugins/<name>, unless its
// marker already names the pack's commit.
func (p *Server) materializePack(ctx context.Context, conn workspacesdk.AgentConn, home string, pack enabledPack) error {
	// Install validates names; checking again keeps a bad row from
	// writing outside managedPackRoot.
	if err := codersdk.NameValid(pack.Name); err != nil {
		return xerrors.Errorf("invalid pack name: %w", err)
	}
	dir := strings.TrimRight(home, `/\`) + managedPackRoot + pack.Name
	marker := dir + "/" + packCommitMarker
	if commit, err := readPackMarker(ctx, conn, marker); err == nil && commit == pack.CommitSHA {
		return nil
	}

	fdb, err := forkdb.From(p.db)
	if err != nil {
		return xerrors.Errorf("fork queries unavailable: %w", err)
	}
	// Org members cannot read packs, and enabling one for a project or
	// chat is what delivers its files to the chat's workspace.
	//nolint:gocritic // chatd reads the content of a pack enabled for this chat.
	resources, err := fdb.GetPluginPackResources(dbauthz.AsSystemRestricted(ctx), pack.ID)
	if err != nil {
		return xerrors.Errorf("get pack resources: %w", err)
	}
	for _, resource := range resources {
		rel, ok := packResourcePath(resource.Path)
		if !ok {
			return xerrors.Errorf("pack resource has unsafe path %q", resource.Path)
		}
		if err := conn.WriteFile(ctx, dir+"/"+rel, bytes.NewReader(resource.Content)); err != nil {
			return xerrors.Errorf("write %q: %w", rel, err)
		}
	}
	if err := conn.WriteFile(ctx, marker, strings.NewReader(pack.CommitSHA)); err != nil {
		return xerrors.Errorf("write commit marker: %w", err)
	}
	return nil
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
