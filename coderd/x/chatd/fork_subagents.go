package chatd

// This file is fork code: it implements nested subagents behind the
// fork-nested-subagents experiment. Upstream's chatd files call the hooks in
// this file (forkValidateSpawnParent, createSubagentChatWithID,
// isSubagentTarget, childGetsOrchestrationPrompt and
// forkSpawnAgentDescription), which keep upstream's behavior when the
// experiment is off.

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
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

// maxSubagentDepth returns the maximum subagent depth of chats in the
// organization: the deployment value, lowered by the organization's max_depth
// when it sets one.
func (p *Server) maxSubagentDepth(ctx context.Context, fdb forkdb.Store, organizationID uuid.UUID) (int, error) {
	maxDepth := p.chatLimits.Fork.MaxSubagentDepth
	//nolint:gocritic // Chat creators cannot read organization config, so chatd reads it.
	limits, err := fdb.GetChatOrganizationSubagentLimits(dbauthz.AsChatd(ctx), organizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return maxDepth, nil
	}
	if err != nil {
		return 0, xerrors.Errorf("get subagent limits of organization %s: %w", organizationID, err)
	}
	if limits.MaxDepth.Valid {
		maxDepth = min(maxDepth, int(limits.MaxDepth.Int32))
	}
	return maxDepth, nil
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
	maxDepth, err := p.maxSubagentDepth(ctx, fdb, parent.OrganizationID)
	if err != nil {
		return err
	}
	_, err = validateNestedSpawnParent(ctx, fdb, parent, maxDepth)
	return err
}

// createSubagentChatWithID creates the child chat of parent. With nested
// subagents enabled, or for a custom agentType, it also records the child's
// chat_subagents row in the same transaction. Nested subagents also recheck
// the depth limit there.
func (p *Server) createSubagentChatWithID(
	ctx context.Context,
	publisher chatstate.Publisher,
	chatID uuid.UUID,
	parent database.Chat,
	agentType string,
	input chatstate.CreateChatInput,
) (chatstate.CreateChatResult, error) {
	nested := p.nestedSubagentsEnabled()
	if !nested && agentType == "" {
		return chatstate.CreateChatWithID(ctx, p.db, publisher, chatID, input)
	}
	// CreateChatWithID publishes into this buffer when its nested
	// transaction returns, so nothing reaches subscribers before the outer
	// transaction commits.
	buffer := chatstate.NewPublishBuffer(publisher)
	defer buffer.Discard()
	var result chatstate.CreateChatResult
	err := forkdb.InTx(p.db, func(tx database.Store, ftx forkdb.Store) error {
		// Without nested subagents the parent is a root chat that was
		// already validated, so only its depth is needed.
		var depth int32
		var err error
		if nested {
			var maxDepth int
			maxDepth, err = p.maxSubagentDepth(ctx, ftx, parent.OrganizationID)
			if err != nil {
				return err
			}
			depth, err = validateNestedSpawnParent(ctx, ftx, parent, maxDepth)
		} else {
			depth, err = subagentDepth(ctx, ftx, parent)
		}
		if err != nil {
			return err
		}
		result, err = chatstate.CreateChatWithID(ctx, tx, buffer, chatID, input)
		if err != nil {
			return err
		}
		_, err = ftx.InsertChatSubagent(ctx, forkdb.InsertChatSubagentParams{
			ChatID:    result.Chat.ID,
			AgentType: cmp.Or(agentType, subagentTypeFromChat(result.Chat)),
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
	return result, buffer.Flush()
}

// subagentCanSpawn reports whether chat may spawn children: explore and
// computer use chats never can, and a general chat can only while its
// children stay within the maximum subagent depth.
func (p *Server) subagentCanSpawn(ctx context.Context, chat database.Chat) (bool, error) {
	if !p.nestedSubagentsEnabled() || !subagentModeCanSpawn(chat.Mode) {
		return false, nil
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return false, xerrors.Errorf("fork queries: %w", err)
	}
	depth, err := subagentDepth(ctx, fdb, chat)
	if err != nil {
		return false, err
	}
	maxDepth, err := p.maxSubagentDepth(ctx, fdb, chat.OrganizationID)
	if err != nil {
		return false, err
	}
	return int(depth)+1 <= maxDepth, nil
}

func subagentModeCanSpawn(mode database.NullChatMode) bool {
	if isExploreSubagentMode(mode) {
		return false
	}
	return !mode.Valid || mode.ChatMode != database.ChatModeComputerUse
}

// nestedSubagentTools returns the subagent tools of a delegated general chat.
// Root chats get them from appendRootChatTools instead. A chat past the
// spawn boundary, which happens when the maximum subagent depth is lowered
// after its children exist, keeps the tools that manage those children:
// targeting is limited to direct children, so no other chat can reach them.
func (p *Server) nestedSubagentTools(ctx context.Context, chat database.Chat, modelConfigID uuid.UUID) ([]fantasy.AgentTool, error) {
	if !chat.ParentChatID.Valid || !p.nestedSubagentsEnabled() || !subagentModeCanSpawn(chat.Mode) {
		return nil, nil
	}
	canSpawn, err := p.subagentCanSpawn(ctx, chat)
	if err != nil {
		return nil, err
	}
	if !canSpawn {
		children, err := p.db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
			ParentIds: []uuid.UUID{chat.ID},
			Archived:  sql.NullBool{Bool: false, Valid: true},
		})
		if err != nil {
			return nil, xerrors.Errorf("get child chats of %s: %w", chat.ID, err)
		}
		if len(children) == 0 {
			return nil, nil
		}
	}
	tools, err := p.subagentTools(ctx, func() database.Chat { return chat }, modelConfigID)
	if err != nil || canSpawn {
		return tools, err
	}
	return slices.DeleteFunc(tools, func(tool fantasy.AgentTool) bool {
		name := tool.Info().Name
		return name == spawnAgentToolName || name == listSubagentModelsToolName
	}), nil
}

// isSubagentTarget reports whether parentChatID may wait on, message,
// interrupt or list targetChatID. Upstream allows any descendant; nested
// subagents allow only direct children, so each level manages its own.
func (p *Server) isSubagentTarget(ctx context.Context, parentChatID, targetChatID uuid.UUID) (bool, error) {
	if !p.nestedSubagentsEnabled() {
		return isSubagentDescendant(ctx, p.db, parentChatID, targetChatID)
	}
	target, err := p.db.GetChatByID(ctx, targetChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("get chat %s: %w", targetChatID, err)
	}
	return target.ParentChatID.Valid && target.ParentChatID.UUID == parentChatID, nil
}

// childGetsOrchestrationPrompt reports whether a child of parent with
// childMode keeps the subagent orchestration guidance, which is true only
// when the child will have the subagent tools.
func (p *Server) childGetsOrchestrationPrompt(ctx context.Context, parent database.Chat, childMode database.NullChatMode) bool {
	if !p.nestedSubagentsEnabled() || !subagentModeCanSpawn(childMode) {
		return false
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		p.logger.Warn(ctx, "fork queries for subagent orchestration prompt", slog.Error(err))
		return false
	}
	depth, err := subagentDepth(ctx, fdb, parent)
	if err != nil {
		p.logger.Warn(ctx, "get parent subagent depth for orchestration prompt",
			slog.F("chat_id", parent.ID), slog.Error(err))
		return false
	}
	maxDepth, err := p.maxSubagentDepth(ctx, fdb, parent.OrganizationID)
	if err != nil {
		p.logger.Warn(ctx, "get maximum subagent depth for orchestration prompt",
			slog.F("chat_id", parent.ID), slog.Error(err))
		return false
	}
	return int(depth)+2 <= maxDepth
}

const upstreamSpawnAgentChildToolsSentence = "The child cannot spawn its own subagents; its tools depend on its type and mode. "

// forkSpawnAgentDescription rewrites upstream's spawn_agent description,
// which says children cannot spawn, when nested subagents are enabled. The
// depth it states is advisory, since spawning enforces it, so a failed
// organization lookup states the deployment value instead of failing the turn.
func (p *Server) forkSpawnAgentDescription(ctx context.Context, chat database.Chat, description string) string {
	if !p.nestedSubagentsEnabled() {
		return description
	}
	maxDepth := p.chatLimits.Fork.MaxSubagentDepth
	fdb, err := forkdb.From(p.db)
	if err == nil {
		maxDepth, err = p.maxSubagentDepth(ctx, fdb, chat.OrganizationID)
	}
	if err != nil {
		p.logger.Warn(ctx, "get maximum subagent depth for spawn_agent description",
			slog.F("chat_id", chat.ID), slog.Error(err))
		maxDepth = p.chatLimits.Fork.MaxSubagentDepth
	}
	return strings.Replace(description, upstreamSpawnAgentChildToolsSentence, fmt.Sprintf(
		"A general child can spawn its own subagents while it is below the maximum subagent depth of %d; "+
			"explore and computer use children cannot. Its tools depend on its type and mode. ",
		maxDepth,
	), 1)
}
