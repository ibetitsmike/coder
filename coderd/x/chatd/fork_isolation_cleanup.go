package chatd

// This file is fork code: after a family archive commits, it removes the git
// worktrees of the family's worktree subagents and stops the workspaces of
// its workspace subagents behind the fork-subagent-isolation experiment, and
// posts a notice on the root chat that lists each worktree it kept.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const worktreeKeptAgentUnreachable = "agent unreachable"

// keptWorktree is a worktree that an archive cleanup did not remove.
type keptWorktree struct {
	path   string
	branch string
	reason string
}

// scheduleArchivedSubagentCleanup removes the worktrees of the worktree
// subagents and stops the workspaces of the workspace subagents in
// familyChats, the chats of a family whose archive committed. It runs in the
// background so that an unreachable agent cannot fail or delay the archive.
func (p *Server) scheduleArchivedSubagentCleanup(ctx context.Context, rootID uuid.UUID, familyChats []database.Chat) {
	if !p.experiments.Enabled(codersdk.ExperimentForkSubagentIsolation) || len(familyChats) == 0 {
		return
	}
	cleanupCtx, stop := p.inflightContext(ctx)
	if err := p.goInflight(func() {
		defer stop()
		p.cleanupArchivedSubagents(cleanupCtx, rootID, familyChats)
	}); err != nil {
		stop()
		p.logger.Error(context.WithoutCancel(ctx), "failed to schedule archived subagent cleanup",
			slog.F("chat_id", rootID), slog.Error(err))
	}
}

func (p *Server) cleanupArchivedSubagents(ctx context.Context, rootID uuid.UUID, familyChats []database.Chat) {
	logger := p.logger.With(slog.F("chat_id", rootID))
	fdb, err := forkdb.From(p.db)
	if err != nil {
		logger.Error(ctx, "subagent worktree cleanup: fork queries", slog.Error(err))
		return
	}

	var (
		root     database.Chat
		kept     []keptWorktree
		agentIDs []uuid.UUID
		byAgent  = map[uuid.UUID][]forkdb.ChatSubagent{}
	)
	for _, chat := range familyChats {
		if chat.ID == rootID {
			root = chat
		}
		row, err := fdb.GetChatSubagentByChatID(ctx, chat.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			logger.Error(ctx, "subagent worktree cleanup: get subagent", slog.F("subagent_id", chat.ID), slog.Error(err))
			continue
		}
		if row.Isolation == subagentIsolationWorkspace && row.WorkspaceID.Valid {
			if err := p.stopSubagentWorkspace(ctx, chat.OwnerID, row.WorkspaceID.UUID); err != nil {
				logger.Warn(ctx, "stop workspace of archived subagent",
					slog.F("subagent_id", chat.ID), slog.F("workspace_id", row.WorkspaceID.UUID), slog.Error(err))
			}
			continue
		}
		if row.Isolation != subagentIsolationWorktree || row.WorktreePath == "" {
			continue
		}
		if !chat.AgentID.Valid || p.agentConnFn == nil {
			kept = append(kept, keptWorktree{path: row.WorktreePath, branch: row.Branch, reason: worktreeKeptAgentUnreachable})
			continue
		}
		if _, ok := byAgent[chat.AgentID.UUID]; !ok {
			agentIDs = append(agentIDs, chat.AgentID.UUID)
		}
		byAgent[chat.AgentID.UUID] = append(byAgent[chat.AgentID.UUID], row)
	}
	// The notice is posted with ctx, so an agent that uses up this deadline
	// still gets its worktrees listed.
	agentCtx, cancel := context.WithTimeout(ctx, worktreeCleanupTimeout)
	defer cancel()
	for _, agentID := range agentIDs {
		kept = append(kept, p.removeAgentWorktrees(agentCtx, agentID, byAgent[agentID])...)
	}
	for _, wt := range kept {
		logger.Warn(ctx, "kept subagent worktree after archive",
			slog.F("path", wt.path), slog.F("branch", wt.branch), slog.F("reason", wt.reason))
	}
	if len(kept) == 0 || root.ID == uuid.Nil {
		return
	}
	if err := p.postKeptWorktreesNotice(ctx, root, kept); err != nil {
		logger.Error(ctx, "post kept subagent worktrees notice", slog.Error(err))
	}
}

// removeAgentWorktrees removes rows' worktrees through agentID and returns
// the ones it kept.
func (p *Server) removeAgentWorktrees(ctx context.Context, agentID uuid.UUID, rows []forkdb.ChatSubagent) []keptWorktree {
	keepAll := func(reason string) []keptWorktree {
		kept := make([]keptWorktree, 0, len(rows))
		for _, row := range rows {
			kept = append(kept, keptWorktree{path: row.WorktreePath, branch: row.Branch, reason: reason})
		}
		return kept
	}
	// A dial to an offline agent waits until its context ends.
	dialCtx, cancelDial := context.WithTimeout(ctx, p.dialTimeout)
	defer cancelDial()
	conn, release, err := p.agentConnFn(dialCtx, agentID)
	if err != nil {
		p.logger.Warn(ctx, "subagent worktree cleanup: connect to workspace agent",
			slog.F("agent_id", agentID), slog.Error(err))
		return keepAll(worktreeKeptAgentUnreachable)
	}
	defer release()
	worktrees, ok := conn.(workspacesdk.ForkWorktreeConn)
	if !ok {
		return keepAll(worktreeKeptAgentUnreachable)
	}
	var kept []keptWorktree
	for _, row := range rows {
		err := worktrees.ForkDeleteWorktree(ctx, workspacesdk.ForkDeleteWorktreeRequest{Path: row.WorktreePath, Branch: row.Branch})
		if reason := worktreeKeptReason(err); reason != "" {
			kept = append(kept, keptWorktree{path: row.WorktreePath, branch: row.Branch, reason: reason})
			continue
		}
		p.logger.Info(ctx, "removed subagent worktree after archive",
			slog.F("subagent_id", row.ChatID), slog.F("path", row.WorktreePath), slog.F("branch", row.Branch))
	}
	return kept
}

// worktreeKeptReason returns why a worktree delete that returned err kept
// the worktree, or "" when the worktree is gone.
func worktreeKeptReason(err error) string {
	if err == nil {
		return ""
	}
	var sdkErr *codersdk.Error
	if !errors.As(err, &sdkErr) {
		return worktreeKeptAgentUnreachable
	}
	if sdkErr.StatusCode() == http.StatusNotFound {
		// The agent answers 404 with this message for a worktree that is
		// already gone, and older agents answer 404 for the whole route.
		if strings.Contains(sdkErr.Message, "does not exist") {
			return ""
		}
		return "the workspace agent is older than the Coder server and cannot remove worktrees"
	}
	reason := strings.TrimSuffix(sdkErr.Message, ".")
	if sdkErr.Detail != "" {
		reason += ": " + sdkErr.Detail
	}
	return reason
}

// postKeptWorktreesNotice appends a notice listing kept to root's history.
// The notice starts no turn and leaves root archived.
func (p *Server) postKeptWorktreesNotice(ctx context.Context, root database.Chat, kept []keptWorktree) error {
	var text strings.Builder
	_, _ = fmt.Fprintf(&text, "Archiving this chat kept %d subagent git worktree(s). Remove them yourself once you no longer need them:\n", len(kept))
	for _, wt := range kept {
		_, _ = fmt.Fprintf(&text, "\n- `%s` on branch `%s`: %s", wt.path, wt.branch, wt.reason)
	}
	content, err := chatprompt.MarshalParts([]codersdk.ChatMessagePart{codersdk.ChatMessageText(text.String())})
	if err != nil {
		return xerrors.Errorf("marshal notice: %w", err)
	}
	notice := chatstate.Message{
		Role:           database.ChatMessageRoleSystem,
		Content:        content,
		Visibility:     database.ChatMessageVisibilityUser,
		ModelConfigID:  uuid.NullUUID{UUID: root.LastModelConfigID, Valid: root.LastModelConfigID != uuid.Nil},
		ContentVersion: chatprompt.CurrentContentVersion,
	}
	return p.newChatMachine(root.ID).Update(ctx, func(tx *chatstate.Tx, _ database.Store) error {
		return tx.ForkAppendNotice(notice)
	})
}
