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
				// The run that spawned it stopped for good, so the child
				// would wait forever without its workspace and prompt.
				t.server.discardSubagent(ctx, child.ID)
			}
			childID = uuid.NewSHA1(childID, []byte(spawnAgentToolName))
			continue
		}

		conflict := new(bool)
		spawnCtx := context.WithValue(context.WithValue(ctx, forkSpawnChildIDKey{}, childID), forkSpawnWorktreeConflictKey{}, conflict)
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
		// An earlier run created the worktree of this ID and stopped before
		// its child existed. The next ID gets a worktree of its own.
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

// forkNoteWorktreeConflict tells a replay safe spawn that the worktree of its
// child chat ID already exists.
func forkNoteWorktreeConflict(ctx context.Context, err error) {
	conflict, ok := ctx.Value(forkSpawnWorktreeConflictKey{}).(*bool)
	var sdkErr *codersdk.Error
	if ok && errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusConflict {
		*conflict = true
	}
}
