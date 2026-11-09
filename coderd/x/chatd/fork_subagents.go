package chatd

// This file is fork code: it implements nested subagents behind the
// fork-nested-subagents experiment. Upstream's subagent_operations.go calls
// forkValidateSpawnParent and createSubagentChatWithID, which keep upstream's
// behavior when the experiment is off.

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

func (p *Server) nestedSubagentsEnabled() bool {
	return p.experiments.Enabled(codersdk.ExperimentForkNestedSubagents)
}

// subagentDepth returns the depth of chat. A root chat is at depth 0. A
// child without a chat_subagents row was created before depths were
// recorded, when only roots could spawn, so it is at depth 1.
func subagentDepth(ctx context.Context, fdb forkdb.Store, chat database.Chat) (int32, error) {
	if !chat.ParentChatID.Valid {
		return 0, nil
	}
	row, err := fdb.GetChatSubagentByChatID(ctx, chat.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 0, xerrors.Errorf("get subagent depth of chat %s: %w", chat.ID, err)
	}
	return row.Depth, nil
}

// validateNestedSpawnParent returns the depth of parent, or an error when
// parent cannot spawn a child within maxDepth.
func validateNestedSpawnParent(ctx context.Context, fdb forkdb.Store, parent database.Chat, maxDepth int) (int32, error) {
	if isExploreSubagentMode(parent.Mode) {
		return 0, xerrors.New("explore chats cannot create child subagents")
	}
	if parent.Mode.Valid && parent.Mode.ChatMode == database.ChatModeComputerUse {
		return 0, xerrors.New("computer use chats cannot create child subagents")
	}
	depth, err := subagentDepth(ctx, fdb, parent)
	if err != nil {
		return 0, err
	}
	if int(depth)+1 > maxDepth {
		return 0, xerrors.Errorf("cannot create a child agent: this chat is at subagent depth %d and the maximum subagent depth is %d", depth, maxDepth)
	}
	return depth, nil
}

func (p *Server) forkValidateSpawnParent(ctx context.Context, parent database.Chat) error {
	if !p.nestedSubagentsEnabled() {
		return validateSubagentSpawnParent(parent)
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return xerrors.Errorf("fork queries: %w", err)
	}
	_, err = validateNestedSpawnParent(ctx, fdb, parent, p.chatLimits.Fork.MaxSubagentDepth)
	return err
}

// createSubagentChatWithID creates the child chat of parent. With nested
// subagents enabled it also records the child's chat_subagents row in the
// same transaction, rechecking the depth limit there. W1 (custom agent types)
// and W4 (isolation) extend this function for the other chat_subagents
// columns.
func (p *Server) createSubagentChatWithID(
	ctx context.Context,
	publisher chatstate.Publisher,
	chatID uuid.UUID,
	parent database.Chat,
	input chatstate.CreateChatInput,
) (chatstate.CreateChatResult, error) {
	if !p.nestedSubagentsEnabled() {
		return chatstate.CreateChatWithID(ctx, p.db, publisher, chatID, input)
	}
	// CreateChatWithID publishes into this buffer when its nested
	// transaction returns, so nothing reaches subscribers before the outer
	// transaction commits.
	buffer := chatstate.NewPublishBuffer(publisher)
	defer buffer.Discard()
	maxDepth := p.chatLimits.Fork.MaxSubagentDepth
	var result chatstate.CreateChatResult
	err := forkdb.InTx(p.db, func(tx database.Store, ftx forkdb.Store) error {
		depth, err := validateNestedSpawnParent(ctx, ftx, parent, maxDepth)
		if err != nil {
			return err
		}
		result, err = chatstate.CreateChatWithID(ctx, tx, buffer, chatID, input)
		if err != nil {
			return err
		}
		_, err = ftx.InsertChatSubagent(ctx, forkdb.InsertChatSubagentParams{
			ChatID:    result.Chat.ID,
			AgentType: subagentTypeFromChat(result.Chat),
			Depth:     depth + 1,
			Isolation: "shared",
		})
		if err != nil {
			return xerrors.Errorf("insert chat subagent: %w", err)
		}
		return nil
	}, nil)
	if err != nil {
		return chatstate.CreateChatResult{}, err
	}
	if err := buffer.Flush(); err != nil {
		return result, err
	}
	return result, nil
}
