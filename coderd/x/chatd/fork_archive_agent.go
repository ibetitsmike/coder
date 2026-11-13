package chatd

// This file is fork code: it holds the archive_agent tool of the
// fork-nested-subagents experiment, which archives one direct child of a
// chat and every descendant of that child. appendForkTools adds the tool to
// a turn that has interrupt_agent. Behind fork-subagent-isolation the tool
// then removes the worktrees and stops the workspaces of the archived
// subagents, with the cleanup that follows a root archive.

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

const archiveAgentToolName = "archive_agent"

type archiveAgentArgs struct {
	ChatID string `json:"chat_id" description:"The ID of a direct child agent of this chat."`
}

// archiveAgentResult keeps its fields in sorted JSON key order, like the
// subagent tool results.
type archiveAgentResult struct {
	ArchivedChatIDs []string                   `json:"archived_chat_ids"`
	ChatID          string                     `json:"chat_id"`
	KeptWorktrees   []archiveAgentKeptWorktree `json:"kept_worktrees,omitempty"`
}

type archiveAgentKeptWorktree struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// serialArchiveAgentTool runs the archive_agent calls of a step one at a
// time, after the concurrent calls of the step settle.
type serialArchiveAgentTool struct {
	fantasy.AgentTool
}

func (serialArchiveAgentTool) SerialToolCalls() bool { return true }

// withForkArchiveAgent adds archive_agent to tools when they hold
// interrupt_agent, so the chats that can stop a child can also retire it.
func (p *Server) withForkArchiveAgent(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	if !slices.ContainsFunc(tools, func(tool fantasy.AgentTool) bool { return tool.Info().Name == "interrupt_agent" }) {
		return tools
	}
	description := "Archive one of your direct child agents and all of its " +
		"descendants for good. It interrupts them first and then archives " +
		"them, so they leave list_agents, no longer count toward the live " +
		"subagent limit of the family, and cannot be messaged or resumed. " +
		"Collect what you need from the child before you archive it. " +
		"Unarchiving the root chat restores them."
	if p.experiments.Enabled(codersdk.ExperimentForkSubagentIsolation) {
		description += " It also removes the git worktree and branch of each " +
			"archived worktree agent when the worktree is clean, lists in " +
			"kept_worktrees each worktree it kept and why, and stops the " +
			"workspace of each archived workspace agent."
	}
	return append(tools, serialArchiveAgentTool{AgentTool: fantasy.NewAgentTool(
		archiveAgentToolName,
		description,
		func(ctx context.Context, args archiveAgentArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			result, err := p.archiveAgent(ctx, chat, args)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return toolJSONResponse(result), nil
		},
	)})
}

// archiveAgent archives args.ChatID, a direct child of parent, and its
// descendants, and cleans up after the subagents it archived.
func (p *Server) archiveAgent(ctx context.Context, parent database.Chat, args archiveAgentArgs) (archiveAgentResult, error) {
	childID, err := parseSubagentToolChatID(args.ChatID)
	if err != nil {
		return archiveAgentResult{}, err
	}
	child, err := p.db.GetChatByID(ctx, childID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return archiveAgentResult{}, xerrors.Errorf("get child chat: %w", err)
	}
	if err != nil || !child.ParentChatID.Valid || child.ParentChatID.UUID != parent.ID {
		return archiveAgentResult{}, xerrors.New("chat_id must be a direct child of this chat")
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return archiveAgentResult{}, xerrors.Errorf("fork queries: %w", err)
	}
	rows, err := fdb.GetChatSubtreeIDs(ctx, child.ID)
	if err != nil {
		return archiveAgentResult{}, xerrors.Errorf("get chat subtree: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows)+1)
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if err := p.settleChatsForArchive(ctx, child, append(ids, child.ID)); err != nil {
		return archiveAgentResult{}, err
	}

	members, archived, err := p.setSubtreeArchived(ctx, subagentRootChatID(parent), child.ID)
	if errors.Is(err, chatstate.ErrTransitionNotAllowed) {
		return archiveAgentResult{}, xerrors.New("the child agent or one of its descendants is still busy or paused; wait for it to stop and try again")
	}
	if err != nil {
		return archiveAgentResult{}, xerrors.Errorf("archive child agent: %w", err)
	}
	p.scheduleArchiveDebugCleanup(ctx, archived)
	p.publishChatPubsubEvents(archived, codersdk.ChatWatchEventKindDeleted)

	result := archiveAgentResult{ArchivedChatIDs: make([]string, 0, len(members)), ChatID: child.ID.String()}
	for _, id := range members {
		result.ArchivedChatIDs = append(result.ArchivedChatIDs, id.String())
	}
	if p.experiments.Enabled(codersdk.ExperimentForkSubagentIsolation) && len(archived) > 0 {
		logger := p.logger.With(slog.F("chat_id", child.ID))
		kept, workspaces := p.removeArchivedSubagentWorktrees(ctx, logger, archived)
		for _, wt := range kept {
			result.KeptWorktrees = append(result.KeptWorktrees, archiveAgentKeptWorktree{Branch: wt.branch, Path: wt.path, Reason: wt.reason})
		}
		p.scheduleArchivedSubagentWorkspaceStops(ctx, logger, workspaces)
	}
	return result, nil
}

// setSubtreeArchived archives topID and its descendants in the family of
// rootID in one transaction, with the lock order of a family archive. It
// returns every chat of the subtree, leaves first, and the chats it
// archived, which leaves out the ones that were archived already. An active
// member fails the archive with chatstate.ErrTransitionNotAllowed.
func (p *Server) setSubtreeArchived(ctx context.Context, rootID, topID uuid.UUID) ([]uuid.UUID, []database.Chat, error) {
	cancelWakeups := p.experiments.Enabled(codersdk.ExperimentForkChatWakeups)
	buffer := chatstate.NewPublishBuffer(p.pubsub)
	defer buffer.Discard()
	var (
		members  []uuid.UUID
		archived []database.Chat
	)
	err := forkdb.InTx(p.db, func(tx database.Store, ftx forkdb.Store) error {
		var err error
		members, err = lockSubtreeForArchive(ctx, tx, ftx, rootID, topID)
		if err != nil {
			return err
		}
		archived = nil
		for _, id := range members {
			current, err := tx.GetChatByID(ctx, id)
			if err != nil {
				return xerrors.Errorf("get chat %s: %w", id, err)
			}
			if current.Archived {
				continue
			}
			var chat database.Chat
			err = chatstate.NewChatMachine(tx, buffer, id).Update(ctx, func(state *chatstate.Tx, _ database.Store) error {
				if _, err := state.SetArchived(chatstate.SetArchivedInput{Archived: true}); err != nil {
					return err
				}
				reloaded, err := state.Store().GetChatByID(state.Ctx(), id)
				if err != nil {
					return xerrors.Errorf("reload archived chat: %w", err)
				}
				chat = reloaded
				return nil
			})
			if err != nil {
				return err
			}
			archived = append(archived, chat)
		}
		if !cancelWakeups {
			return nil
		}
		return cancelArchivedWakeups(ctx, ftx, archived, dbtime.Time(p.clock.Now()).UTC())
	}, nil)
	if err != nil {
		return nil, nil, err
	}
	return members, archived, buffer.Flush()
}

// scheduleArchivedSubagentWorkspaceStops stops workspaces in the background,
// because a stop waits for its build.
func (p *Server) scheduleArchivedSubagentWorkspaceStops(ctx context.Context, logger slog.Logger, workspaces []archivedSubagentWorkspace) {
	if len(workspaces) == 0 {
		return
	}
	stopCtx, stop := p.inflightContext(ctx)
	if err := p.goInflight(func() {
		defer stop()
		p.stopArchivedSubagentWorkspaces(stopCtx, logger, workspaces)
	}); err != nil {
		stop()
		logger.Error(context.WithoutCancel(ctx), "failed to schedule archived subagent workspace stops", slog.Error(err))
	}
}
