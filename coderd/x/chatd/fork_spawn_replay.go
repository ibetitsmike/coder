package chatd

// This file is fork code: it makes a replayed spawn_agent call return the
// child it already created, behind the fork-nested-subagents experiment.
// When chatd stops in the middle of a tool batch, the next worker runs the
// whole batch again with the same tool call IDs. Without this wrapper each
// spawn_agent call in that batch would create a second child.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// maxSpawnReplayAttempts bounds the child chat IDs that one spawn_agent call
// tries: the first comes from the tool call ID and each later one from the
// one before, when an earlier run left that ID unusable.
const maxSpawnReplayAttempts = 4

// forkSpawnChildIDKey carries the child chat ID that a spawn_agent call
// derives from its tool call ID.
type forkSpawnChildIDKey struct{}

// forkSpawnWorktreeConflictKey carries a flag that createSubagentWorktree
// sets when the worktree of the derived child chat ID already exists.
type forkSpawnWorktreeConflictKey struct{}

// forkSpawnOwnerKey carries the calling chat as its turn loaded it, owned by
// the worker and runner of that turn.
type forkSpawnOwnerKey struct{}

// forkReplaySafeSpawnTool wraps spawn_agent so that a call with a tool call
// ID creates its child with an ID derived from that tool call ID, and a
// replay of the call returns that child instead of creating another one.
type forkReplaySafeSpawnTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withForkReplaySafeSpawn wraps every spawn_agent tool in tools with
// forkReplaySafeSpawnTool.
func (p *Server) withForkReplaySafeSpawn(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	if !p.nestedSubagentsEnabled() {
		return tools
	}
	for i, tool := range tools {
		if tool.Info().Name == spawnAgentToolName {
			tools[i] = forkReplaySafeSpawnTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

// spawnedChildState is what an earlier run of a spawn_agent call left at a
// derived child chat ID.
type spawnedChildState int

const (
	// spawnedChildAbsent: no chat has the ID.
	spawnedChildAbsent spawnedChildState = iota
	// spawnedChildReady: a started, unarchived child of the caller has it.
	spawnedChildReady
	// spawnedChildRetired: a child of the caller has it, but it is archived
	// or its spawn stopped before the child got its workspace and prompt.
	spawnedChildRetired
	// spawnedChildTaken: a chat of another parent has it.
	spawnedChildTaken
)

func (t forkReplaySafeSpawnTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	callID, ok := workspacesdk.ToolCallIDFromContext(ctx)
	if !ok {
		return t.AgentTool.Run(ctx, call)
	}
	childID := uuid.NewSHA1(callID, []byte(spawnAgentToolName))
	for range maxSpawnReplayAttempts {
		child, state, err := t.spawnedChild(ctx, childID)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		switch state {
		case spawnedChildReady:
			return toolJSONResponse(subagentChatResult(child)), nil
		case spawnedChildTaken:
			return fantasy.NewTextErrorResponse("spawn_agent: the child chat ID of this call is taken by another chat"), nil
		case spawnedChildRetired:
			if !child.Archived {
				// The run that spawned it stopped, so the child would wait
				// forever without its workspace and prompt. A run that still
				// goes on may record the workspace meanwhile; then the
				// child is started and the replay returns it.
				kept, err := t.server.discardSubagentUnless(ctx, child.ID, forkSubagentStarted(child.ID))
				if err != nil {
					return fantasy.NewTextErrorResponse("spawn_agent: retire the unstarted child: " + err.Error()), nil
				}
				if kept {
					continue
				}
			}
			childID = uuid.NewSHA1(childID, []byte(spawnAgentToolName))
			continue
		}

		conflict := new(bool)
		spawnCtx := context.WithValue(ctx, forkSpawnChildIDKey{}, childID)
		spawnCtx = context.WithValue(spawnCtx, forkSpawnWorktreeConflictKey{}, conflict)
		spawnCtx = context.WithValue(spawnCtx, forkSpawnOwnerKey{}, t.chat)
		resp, err := t.AgentTool.Run(spawnCtx, call)
		if err != nil || !resp.IsError {
			return resp, err
		}
		// Another run of this call may have created the child meanwhile,
		// and its insert won.
		if child, state, err := t.spawnedChild(ctx, childID); err == nil && state == spawnedChildReady {
			return toolJSONResponse(subagentChatResult(child)), nil
		}
		if !*conflict {
			return resp, nil
		}
		// An earlier run created the worktree of this ID and has no child.
		// If that run still goes on, forkCheckSpawnOwner refuses its child,
		// because this turn owns the chat now. The next ID gets a worktree of
		// its own.
		childID = uuid.NewSHA1(childID, []byte(spawnAgentToolName))
	}
	return fantasy.NewTextErrorResponse("spawn_agent: earlier runs of this call left no usable child; call spawn_agent again"), nil
}

// spawnedChild returns the chat at childID and what state it is in for the
// calling chat.
func (t forkReplaySafeSpawnTool) spawnedChild(ctx context.Context, childID uuid.UUID) (database.Chat, spawnedChildState, error) {
	child, err := t.server.db.GetChatByID(ctx, childID)
	if errors.Is(err, sql.ErrNoRows) {
		return database.Chat{}, spawnedChildAbsent, nil
	}
	if err != nil {
		return database.Chat{}, 0, xerrors.Errorf("spawn_agent: get child chat: %w", err)
	}
	if !child.ParentChatID.Valid || child.ParentChatID.UUID != t.chat.ID {
		return child, spawnedChildTaken, nil
	}
	if child.Archived {
		return child, spawnedChildRetired, nil
	}
	fdb, err := forkdb.From(t.server.db)
	if err != nil {
		return database.Chat{}, 0, xerrors.Errorf("spawn_agent: fork queries: %w", err)
	}
	row, err := fdb.GetChatSubagentByChatID(ctx, childID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return child, spawnedChildReady, nil
	case err != nil:
		return database.Chat{}, 0, xerrors.Errorf("spawn_agent: get chat subagent: %w", err)
	case row.Isolation == subagentIsolationWorkspace && !row.WorkspaceID.Valid:
		// startSubagentWorkspace records the workspace and sends the prompt
		// in one transaction, after the child was created.
		return child, spawnedChildRetired, nil
	}
	return child, spawnedChildReady, nil
}

// forkSubagentStarted reports, under the lock of the child chatID, whether
// startSubagentWorkspace recorded its workspace.
func forkSubagentStarted(chatID uuid.UUID) func(context.Context, database.Store) (bool, error) {
	return func(ctx context.Context, store database.Store) (bool, error) {
		ftx, err := forkdb.From(store)
		if err != nil {
			return false, xerrors.Errorf("fork queries: %w", err)
		}
		row, err := ftx.GetChatSubagentByChatID(ctx, chatID)
		if err != nil {
			return false, xerrors.Errorf("get chat subagent: %w", err)
		}
		return row.WorkspaceID.Valid, nil
	}
}

// forkNoteWorktreeConflict tells a replay safe spawn that the worktree of its
// child chat ID already exists.
func forkNoteWorktreeConflict(ctx context.Context, err error) {
	conflict, ok := ctx.Value(forkSpawnWorktreeConflictKey{}).(*bool)
	var sdkErr *codersdk.Error
	if ok && errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusConflict {
		*conflict = true
	}
}

// forkCheckSpawnOwner runs in the transaction that inserts the child of a
// replay safe spawn. It refuses the child when the turn that made the call
// no longer owns the parent chat: a takeover replays the call, and the new
// turn creates the child instead. The share lock on the parent makes an
// acquire of the parent wait until the child commits, so the replay sees it.
func forkCheckSpawnOwner(ctx context.Context, tx database.Store, parent database.Chat) error {
	owner, ok := ctx.Value(forkSpawnOwnerKey{}).(database.Chat)
	if !ok || owner.ID != parent.ID || !owner.WorkerID.Valid || !owner.RunnerID.Valid {
		return nil
	}
	// Family operations lock the root first. This runs before any other lock
	// of the spawn, so a send that holds the parent and waits for the family
	// admission lock never waits for this transaction.
	if rootID := subagentRootChatID(parent); rootID != parent.ID {
		if _, err := tx.GetChatByIDForShare(ctx, rootID); err != nil {
			return xerrors.Errorf("lock root chat: %w", err)
		}
	}
	current, err := tx.GetChatByIDForShare(ctx, parent.ID)
	if err != nil {
		return xerrors.Errorf("lock parent chat: %w", err)
	}
	if current.WorkerID != owner.WorkerID || current.RunnerID != owner.RunnerID {
		return xerrors.New("spawn_agent: another run took over this chat, so this run creates no child")
	}
	return nil
}
