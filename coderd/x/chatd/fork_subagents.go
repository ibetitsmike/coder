package chatd

// This file is fork code: it implements nested subagents behind the
// fork-nested-subagents experiment. Upstream's chatd files call the hooks in
// this file (forkValidateSpawnParent, createSubagentChatWithID,
// forkValidateSubagentResume, withSubagentResume, isSubagentTarget,
// childGetsOrchestrationPrompt and forkSpawnAgentDescription), which keep
// upstream's behavior when the experiment is off.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
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
	limits, err := p.subagentLimits(ctx, fdb, parent.OrganizationID)
	if err != nil {
		return err
	}
	if _, err := validateNestedSpawnParent(ctx, fdb, parent, limits.maxDepth); err != nil {
		return err
	}
	return validateFamilyLiveSubagents(ctx, fdb, subagentRootChatID(parent), limits.maxLivePerFamily, "create a child agent")
}

type subagentResumeKey struct{}

// withSubagentResume marks ctx as a message_agent send, so SendMessage
// applies the family live subagent limit when the message resumes a finished
// child. Messages that the owner sends through the chat API do not carry it.
func withSubagentResume(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentResumeKey{}, true)
}

// ErrSecretQuestionPending means that the chat waits for a secret answer, so
// a message would answer its question without the value.
var ErrSecretQuestionPending = xerrors.New("the chat waits for a secret answer, which a chat message cannot carry")

type secretAnswerReportKey struct{}

// WithSecretAnswerReport marks ctx as the send of the message that reports a
// stored secret answer, the one message that may answer a secret question.
func WithSecretAnswerReport(ctx context.Context) context.Context {
	return context.WithValue(ctx, secretAnswerReportKey{}, true)
}

// forkValidateSubagentResume returns an error when a message_agent send would
// resume the finished subagent target while its family already has the
// maximum number of live subagents. sendSubagentMessage calls it before
// SendMessage runs the prompt hooks, and SendMessage calls it again with inTx
// in the transaction that resumes target. That call takes the lock that
// serializes the family's spawns, so a concurrent spawn cannot pass the same
// count. It also refuses a send to a target whose pending question is
// secret, because the message would answer the question without the value.
// In the transaction it refuses every such send but the report of the
// answer, because a question can be recorded after the caller's own check.
func (p *Server) forkValidateSubagentResume(ctx context.Context, store database.Store, target database.Chat, inTx bool) error {
	if !p.nestedSubagentsEnabled() || !target.ParentChatID.Valid {
		return nil
	}
	resume, _ := ctx.Value(subagentResumeKey{}).(bool)
	report, _ := ctx.Value(secretAnswerReportKey{}).(bool)
	if !resume && (!inTx || report) {
		return nil
	}
	fdb, err := forkdb.From(store)
	if err != nil {
		return xerrors.Errorf("fork queries: %w", err)
	}
	question, err := fdb.GetPendingChatDescendantQuestion(ctx, target.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return xerrors.Errorf("get pending question of chat %s: %w", target.ID, err)
	case question.Secret() && resume:
		return xerrors.New("cannot message the child agent: it waits for a secret answer, which only the user can give in the root chat")
	case question.Secret():
		return ErrSecretQuestionPending
	}
	if !resume {
		return nil
	}
	if target.Status != database.ChatStatusWaiting && target.Status != database.ChatStatusError {
		return nil
	}
	limits, err := p.subagentLimits(ctx, fdb, target.OrganizationID)
	if err != nil {
		return err
	}
	if limits.maxLivePerFamily == 0 {
		return nil
	}
	rootChatID := subagentRootChatID(target)
	if inTx {
		if err := store.AcquireLock(ctx, chatFamilyAdmissionLockID(rootChatID)); err != nil {
			return xerrors.Errorf("acquire chat family admission lock: %w", err)
		}
	}
	err = validateFamilyLiveSubagents(ctx, fdb, rootChatID, limits.maxLivePerFamily, "resume the child agent")
	if err != nil && !inTx {
		// A concurrent message_agent may have resumed target after the
		// caller loaded it. The send then only queues on a live child, and
		// the transactional check reloads target and skips the limit.
		current, getErr := store.GetChatByID(ctx, target.ID)
		if getErr == nil && current.Status != database.ChatStatusWaiting && current.Status != database.ChatStatusError {
			return nil
		}
	}
	return err
}

// chatFamilyAdmissionLockID is the transaction lock that serializes the
// spawns and resumes of one chat family, so two of them cannot both pass the
// live subagent count.
func chatFamilyAdmissionLockID(rootChatID uuid.UUID) int64 {
	return database.GenLockID("fork_chat_family_admission:" + rootChatID.String())
}

// validateFamilyLiveSubagents returns an error when the family of rootChatID
// already has maxLive live subagents. 0 means no limit.
func validateFamilyLiveSubagents(ctx context.Context, fdb forkdb.Store, rootChatID uuid.UUID, maxLive int, action string) error {
	if maxLive == 0 {
		return nil
	}
	live, err := fdb.CountLiveSubagentsInFamily(ctx, rootChatID)
	if err != nil {
		return xerrors.Errorf("count live subagents of chat family %s: %w", rootChatID, err)
	}
	if live >= int64(maxLive) {
		return xerrors.Errorf("cannot %s: this chat family has %d live subagents and the maximum is %d", action, live, maxLive)
	}
	return nil
}

// createSubagentChatWithID creates the child chat of parent. With nested
// subagents enabled, for a customAgent, or for a spawn that asks for
// isolation, it also records the child's chat_subagents row in the same
// transaction, with the custom agent's tool policy and the child's worktree.
// Nested subagents also recheck the depth and live subagent limits there. A
// worktree is created before the transaction and removed if it fails.
func (p *Server) createSubagentChatWithID(
	ctx context.Context,
	publisher chatstate.Publisher,
	chatID uuid.UUID,
	parent database.Chat,
	customAgent *workspacesdk.AgentDefinition,
	input chatstate.CreateChatInput,
) (chatstate.CreateChatResult, error) {
	nested := p.nestedSubagentsEnabled()
	isolation, isolated := subagentIsolationFromContext(ctx)
	if !isolated && p.experiments.Enabled(codersdk.ExperimentForkSubagentIsolation) {
		worktree, bound, err := p.chatWorktree(ctx, parent)
		if err != nil {
			return chatstate.CreateChatResult{}, xerrors.Errorf("resolve parent worktree: %w", err)
		}
		if bound {
			if input.InitialMessages, err = withSubagentWorktreePrompt(input, worktree, true); err != nil {
				return chatstate.CreateChatResult{}, err
			}
		}
	}
	if !nested && customAgent == nil && !isolated {
		return chatstate.CreateChatWithID(ctx, p.db, publisher, chatID, input)
	}
	var toolPolicy pqtype.NullRawMessage
	if customAgent != nil {
		raw, err := json.Marshal(customAgentToolPolicyFromDefinition(*customAgent))
		if err != nil {
			return chatstate.CreateChatResult{}, xerrors.Errorf("marshal tool policy of agent %q: %w", customAgent.Name, err)
		}
		toolPolicy = pqtype.NullRawMessage{RawMessage: raw, Valid: true}
	}
	subagent := forkdb.InsertChatSubagentParams{Isolation: subagentIsolationShared, ToolPolicy: toolPolicy}
	committed := false
	if isolated {
		worktree, done, err := p.createSubagentWorktree(ctx, chatID, parent, input, isolation)
		if err != nil {
			return chatstate.CreateChatResult{}, err
		}
		defer func() { done(committed) }()
		subagent.Isolation = subagentIsolationWorktree
		subagent.WorktreePath = worktree.Path
		subagent.Branch = worktree.Branch
		input.InitialMessages, err = withSubagentWorktreePrompt(input, subagentWorktree{path: worktree.Path, branch: worktree.Branch}, false)
		if err != nil {
			return chatstate.CreateChatResult{}, err
		}
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
		var limits forkSubagentLimits
		var err error
		if nested {
			limits, err = p.subagentLimits(ctx, ftx, parent.OrganizationID)
			if err != nil {
				return err
			}
			depth, err = validateNestedSpawnParent(ctx, ftx, parent, limits.maxDepth)
		} else {
			depth, err = subagentDepth(ctx, ftx, parent)
		}
		if err != nil {
			return err
		}
		if limits.maxLivePerFamily > 0 {
			rootChatID := subagentRootChatID(parent)
			// Family operations lock the root first, and CreateChatWithID
			// locks it too. Taking it before the admission lock keeps that
			// order, so this spawn never holds the admission lock while it
			// waits for the root.
			if _, err := tx.GetChatByIDForShare(ctx, rootChatID); err != nil {
				return xerrors.Errorf("lock root chat: %w", err)
			}
			if err := tx.AcquireLock(ctx, chatFamilyAdmissionLockID(rootChatID)); err != nil {
				return xerrors.Errorf("acquire chat family admission lock: %w", err)
			}
			if err := validateFamilyLiveSubagents(ctx, ftx, rootChatID, limits.maxLivePerFamily, "create a child agent"); err != nil {
				return err
			}
		}
		result, err = chatstate.CreateChatWithID(ctx, tx, buffer, chatID, input)
		if err != nil {
			return err
		}
		agentType := subagentTypeFromChat(result.Chat)
		if customAgent != nil {
			agentType = customAgent.Name
		}
		subagent.ChatID = result.Chat.ID
		subagent.AgentType = agentType
		subagent.Depth = depth + 1
		_, err = ftx.InsertChatSubagent(ctx, subagent)
		if err != nil {
			return xerrors.Errorf("insert chat subagent: %w", err)
		}
		return nil
	}, nil)
	if err != nil {
		return chatstate.CreateChatResult{}, err
	}
	committed = true
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
	limits, err := p.subagentLimits(ctx, fdb, chat.OrganizationID)
	if err != nil {
		return false, err
	}
	return int(depth)+1 <= limits.maxDepth, nil
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
// childMode and customAgent (nil for built-in types) keeps the subagent
// orchestration guidance, which is true only when the child will have the
// subagent tools.
func (p *Server) childGetsOrchestrationPrompt(
	ctx context.Context,
	parent database.Chat,
	childMode database.NullChatMode,
	customAgent *workspacesdk.AgentDefinition,
) bool {
	if !p.nestedSubagentsEnabled() || !subagentModeCanSpawn(childMode) {
		return false
	}
	if customAgent != nil && !customAgentToolPolicyFromDefinition(*customAgent).keepsOrchestrationTools() {
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
	limits, err := p.subagentLimits(ctx, fdb, parent.OrganizationID)
	if err != nil {
		p.logger.Warn(ctx, "get maximum subagent depth for orchestration prompt",
			slog.F("chat_id", parent.ID), slog.Error(err))
		return false
	}
	return int(depth)+2 <= limits.maxDepth
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
		var limits forkSubagentLimits
		limits, err = p.subagentLimits(ctx, fdb, chat.OrganizationID)
		maxDepth = limits.maxDepth
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
