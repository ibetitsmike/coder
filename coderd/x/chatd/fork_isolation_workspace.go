package chatd

// This file is fork code: it gives a subagent spawned with isolation
// workspace its own workspace, built from the parent workspace's template
// with the parent's build parameters. createSubagentChatWithID creates the
// child waiting and without its prompt, and startSubagentWorkspace sends the
// prompt once the workspace is ready, so the child's first turn can use it.

import (
	"context"
	"encoding/json"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/codersdk"
)

const subagentIsolationWorkspace = "workspace"

// subagentWorkspaceSource is what the workspace of a child spawned with
// isolation workspace is created from.
type subagentWorkspaceSource struct {
	TemplateID uuid.UUID         `json:"template_id"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

// subagentWorkspaceSourceOf returns the template and latest build parameters
// of parent's workspace.
func (p *Server) subagentWorkspaceSourceOf(ctx context.Context, parent database.Chat, input chatstate.CreateChatInput) (subagentWorkspaceSource, error) {
	if input.Mode.Valid && input.Mode.ChatMode == database.ChatModeComputerUse {
		return subagentWorkspaceSource{}, xerrors.New("isolation workspace is not supported for computer use agents")
	}
	if !parent.WorkspaceID.Valid {
		return subagentWorkspaceSource{}, xerrors.New("isolation workspace copies this chat's workspace, and this chat has none")
	}
	if p.createWorkspaceFn == nil {
		return subagentWorkspaceSource{}, xerrors.New("isolation workspace needs workspace creation, which is not configured")
	}
	owner, err := p.chatOwnerSubject(ctx, parent.OwnerID)
	if err != nil {
		return subagentWorkspaceSource{}, err
	}
	//nolint:gocritic // The child's workspace is built as the chat owner.
	ownerCtx := dbauthz.As(ctx, owner)
	workspace, err := p.db.GetWorkspaceByID(ownerCtx, parent.WorkspaceID.UUID)
	if err != nil {
		return subagentWorkspaceSource{}, xerrors.Errorf("get workspace: %w", err)
	}
	build, err := p.db.GetLatestWorkspaceBuildByWorkspaceID(ownerCtx, workspace.ID)
	if err != nil {
		return subagentWorkspaceSource{}, xerrors.Errorf("get latest workspace build: %w", err)
	}
	parameters, err := p.db.GetWorkspaceBuildParameters(ownerCtx, build.ID)
	if err != nil {
		return subagentWorkspaceSource{}, xerrors.Errorf("get workspace build parameters: %w", err)
	}
	source := subagentWorkspaceSource{TemplateID: workspace.TemplateID, Parameters: make(map[string]string, len(parameters))}
	for _, parameter := range parameters {
		source.Parameters[parameter.Name] = parameter.Value
	}
	return source, nil
}

// withDeferredSubagentPrompt returns input for a child that starts waiting,
// with no workspace, and the prompt that startSubagentWorkspace sends later.
func withDeferredSubagentPrompt(input chatstate.CreateChatInput) (chatstate.CreateChatInput, chatstate.Message, error) {
	last := len(input.InitialMessages) - 1
	if last < 0 || input.InitialMessages[last].Role != database.ChatMessageRoleUser {
		return input, chatstate.Message{}, xerrors.New("the child's initial messages do not end with its prompt")
	}
	prompt := input.InitialMessages[last]
	input.InitialMessages = input.InitialMessages[:last]
	input.InitialStatus = database.ChatStatusWaiting
	input.WorkspaceID = uuid.NullUUID{}
	input.BuildID = uuid.NullUUID{}
	input.AgentID = uuid.NullUUID{}
	return input, prompt, nil
}

// startSubagentWorkspace creates the workspace of child with the
// create_workspace tool, which waits for the build and the agent, then
// records the workspace and sends child its prompt. On failure it archives
// child and stops any workspace it got.
func (p *Server) startSubagentWorkspace(ctx context.Context, child database.Chat, source subagentWorkspaceSource, prompt chatstate.Message) (database.Chat, error) {
	started, err := p.createSubagentWorkspace(ctx, child, source, prompt)
	if err != nil {
		p.discardSubagent(ctx, child.ID)
		return database.Chat{}, err
	}
	p.publishChatPubsubEvent(started, codersdk.ChatWatchEventKindStatusChange, nil)
	return started, nil
}

func (p *Server) createSubagentWorkspace(ctx context.Context, child database.Chat, source subagentWorkspaceSource, prompt chatstate.Message) (database.Chat, error) {
	input, err := json.Marshal(source)
	if err != nil {
		return database.Chat{}, xerrors.Errorf("marshal workspace request: %w", err)
	}
	tool := chattool.CreateWorkspace(p.db, child.OrganizationID, child.ID, chattool.CreateWorkspaceOptions{
		OwnerID:                        child.OwnerID,
		CreateFn:                       p.createWorkspaceFn,
		AgentConnFn:                    chattool.AgentConnFunc(p.agentConnFn),
		AgentInactiveDisconnectTimeout: p.agentInactiveDisconnectTimeout,
		Logger:                         p.logger,
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: child.ID.String(), Name: tool.Info().Name, Input: string(input)})
	if err != nil {
		return database.Chat{}, xerrors.Errorf("create the child's workspace: %w", err)
	}
	// create_workspace reports most failures as a JSON result, not as an
	// error response. It also reports a created workspace whose agent is
	// missing or not ready, or whose startup scripts did not finish, which
	// the child could not use.
	var result struct {
		Created        bool   `json:"created"`
		AgentStatus    string `json:"agent_status"`
		StartupScripts string `json:"startup_scripts"`
	}
	if resp.IsError || json.Unmarshal([]byte(resp.Content), &result) != nil ||
		!result.Created || result.AgentStatus != "" || result.StartupScripts != "" {
		return database.Chat{}, xerrors.Errorf("create the child's workspace: %s", resp.Content)
	}

	var started database.Chat
	err = p.newChatMachine(child.ID).Update(ctx, func(tx *chatstate.Tx, store database.Store) error {
		chat, err := store.GetChatByID(ctx, child.ID)
		if err != nil {
			return xerrors.Errorf("get child chat: %w", err)
		}
		if chat.Archived {
			// A replay of the spawn retired the child meanwhile.
			return xerrors.New("create the child's workspace: the child was archived meanwhile")
		}
		if !chat.WorkspaceID.Valid {
			return xerrors.New("create the child's workspace: the child was not bound to it")
		}
		ftx, err := forkdb.From(store)
		if err != nil {
			return xerrors.Errorf("fork queries: %w", err)
		}
		if err := ftx.UpdateChatSubagentWorkspaceID(ctx, forkdb.UpdateChatSubagentWorkspaceIDParams{
			ChatID:      child.ID,
			WorkspaceID: chat.WorkspaceID,
		}); err != nil {
			return xerrors.Errorf("record the child's workspace: %w", err)
		}
		if _, err := tx.SendMessage(chatstate.SendMessageInput{
			Message:      prompt,
			BusyBehavior: chatstate.BusyBehaviorQueue,
			MaxQueueSize: p.chatLimits.MaxQueuedMessagesPerChat,
		}); err != nil {
			return xerrors.Errorf("send the child its prompt: %w", err)
		}
		started, err = store.GetChatByID(ctx, child.ID)
		if err != nil {
			return xerrors.Errorf("reload child chat: %w", err)
		}
		return nil
	})
	if err != nil {
		return database.Chat{}, err
	}
	return started, nil
}

// discardSubagent archives the child chatID whose workspace failed and stops
// the workspace in the background if it has one. Both are best-effort.
func (p *Server) discardSubagent(ctx context.Context, chatID uuid.UUID) {
	if _, err := p.discardSubagentUnless(ctx, chatID, nil); err != nil {
		p.logger.Warn(ctx, "archive subagent whose workspace failed", slog.F("chat_id", chatID), slog.Error(err))
	}
}

// discardSubagentUnless archives the child chatID unless keep, run under the
// lock of the child, reports true. It then stops the workspace of the child
// in the background if it has one, also when the child was archived before,
// because a spawn can bind a workspace to a child that a replay archived. It
// reports whether keep kept the child.
func (p *Server) discardSubagentUnless(ctx context.Context, chatID uuid.UUID, keep func(context.Context, database.Store) (bool, error)) (bool, error) {
	// The spawn may have failed because its context was canceled, and the
	// child must still go.
	ctx = context.WithoutCancel(ctx)
	var (
		archived database.Chat
		kept     bool
		newly    bool
	)
	err := p.newChatMachine(chatID).Update(ctx, func(tx *chatstate.Tx, store database.Store) error {
		if keep != nil {
			var err error
			if kept, err = keep(ctx, store); err != nil || kept {
				return err
			}
		}
		chat, err := store.GetChatByID(ctx, chatID)
		if err != nil {
			return err
		}
		if !chat.Archived {
			if _, err := tx.SetArchived(chatstate.SetArchivedInput{Archived: true}); err != nil {
				return err
			}
			newly = true
		}
		archived, err = store.GetChatByID(ctx, chatID)
		return err
	})
	if err != nil || kept {
		return kept, err
	}
	if newly {
		p.publishChatPubsubEvent(archived, codersdk.ChatWatchEventKindDeleted, nil)
	}
	if !archived.WorkspaceID.Valid || p.stopWorkspaceFn == nil {
		return false, nil
	}
	// The stop can wait for the create build that the spawn gave up on.
	stopCtx, stop := p.inflightContext(ctx)
	if err := p.goInflight(func() {
		defer stop()
		if err := p.stopSubagentWorkspace(stopCtx, archived); err != nil {
			p.logger.Warn(stopCtx, "stop workspace of subagent that failed to start",
				slog.F("chat_id", chatID), slog.F("workspace_id", archived.WorkspaceID.UUID), slog.Error(err))
		}
	}); err != nil {
		stop()
		p.logger.Warn(ctx, "schedule stop of subagent workspace", slog.F("chat_id", chatID), slog.Error(err))
	}
	return false, nil
}

// stopSubagentWorkspace stops the workspace of child with the stop_workspace
// tool, which first waits for an active build and does nothing when the
// workspace is already stopped.
func (p *Server) stopSubagentWorkspace(ctx context.Context, child database.Chat) error {
	tool := chattool.StopWorkspace(p.db, child.ID, chattool.StopWorkspaceOptions{
		OwnerID: child.OwnerID,
		StopFn:  p.stopWorkspaceFn,
		Logger:  p.logger,
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: child.ID.String(), Name: tool.Info().Name, Input: "{}"})
	if err != nil {
		return xerrors.Errorf("stop workspace: %w", err)
	}
	var result struct {
		Stopped bool `json:"stopped"`
	}
	if resp.IsError || json.Unmarshal([]byte(resp.Content), &result) != nil || !result.Stopped {
		return xerrors.Errorf("stop workspace: %s", resp.Content)
	}
	return nil
}

// subagentWorkspaceName returns the name of the workspace of a child spawned
// with isolation workspace, or "" when it has none yet.
func (p *Server) subagentWorkspaceName(ctx context.Context, row forkdb.ChatSubagent) (string, error) {
	if !row.WorkspaceID.Valid {
		return "", nil
	}
	workspace, err := p.db.GetWorkspaceByID(ctx, row.WorkspaceID.UUID)
	if err != nil {
		return "", xerrors.Errorf("get workspace of subagent %s: %w", row.ChatID, err)
	}
	return workspace.Name, nil
}

// addSubagentWorkspaceName sets workspace_name in a spawn_agent or
// wait_agent result, or workspace_error when the lookup fails.
func (p *Server) addSubagentWorkspaceName(ctx context.Context, result map[string]any, row forkdb.ChatSubagent) {
	name, err := p.subagentWorkspaceName(ctx, row)
	if err != nil {
		result["workspace_error"] = err.Error()
		return
	}
	result["workspace_name"] = name
}
