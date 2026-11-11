package chatd

// This file is fork code: it lets a root chat start as an agent type behind
// the fork-agent-definitions experiment. CreateChat stores the type's
// chat_subagents row with the chat, so its tool policy holds from turn one.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	skillspkg "github.com/coder/coder/v2/coderd/x/skills"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// rootAgentWorkspaceReason explains why a workspace definition cannot start
// a chat: a chat pins its workspace's definitions only after the workspace
// agent connects, which is after the chat is created.
const rootAgentWorkspaceReason = "agent definitions from the workspace can only be used through spawn_agent for now"

// RootAgent is the agent type that a root chat starts as.
type RootAgent struct {
	// Name is the agent type stored on the chat, as spawn_agent stores it.
	Name string
	// ModelConfigID is the definition's model. It is unset when the
	// definition sets none or the request chose a model.
	ModelConfigID uuid.NullUUID
	// ReasoningEffort is the definition's reasoning effort, or "".
	ReasoningEffort string

	mode       database.NullChatMode
	prompt     string
	toolPolicy pqtype.NullRawMessage
	// noOrchestration drops the orchestration guidance from a chat that
	// has no subagent tools to follow it with.
	noOrchestration bool
}

// RootAgentOptions selects the agent type of a new root chat.
type RootAgentOptions struct {
	OwnerID        uuid.UUID
	OrganizationID uuid.UUID
	ProjectID      uuid.NullUUID
	AgentType      string
	// ModelConfigID is the chat's model. It breaks ties between models
	// that share the definition's model alias.
	ModelConfigID uuid.UUID
	// ExplicitModel reports that the request chose ModelConfigID, which
	// then wins over the definition's model.
	ExplicitModel bool
}

// RootAgentError reports an agent type that a root chat cannot start as.
type RootAgentError struct {
	Reason string
}

func (e *RootAgentError) Error() string {
	return e.Reason
}

// ResolveRootAgent resolves opts.AgentType like spawn_agent does, from the
// built-in general and explore types and the owner's personal, organization
// and project definitions. Workspace definitions are not pinned yet when a
// chat is created, so they cannot start one.
func (p *Server) ResolveRootAgent(ctx context.Context, opts RootAgentOptions) (RootAgent, error) {
	id := strings.TrimSpace(opts.AgentType)
	switch id {
	case subagentTypeGeneral:
		return RootAgent{Name: id}, nil
	case subagentTypeExplore:
		return RootAgent{Name: id, mode: database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true}, noOrchestration: true}, nil
	}
	if _, ok := lookupSubagentDefinition(id); ok {
		return RootAgent{}, &RootAgentError{Reason: fmt.Sprintf("type %q cannot start a chat", id)}
	}
	if strings.HasPrefix(id, string(skillspkg.SourceWorkspace)+"/") {
		return RootAgent{}, &RootAgentError{Reason: rootAgentWorkspaceReason}
	}
	chat := database.Chat{
		ID:                uuid.New(),
		OwnerID:           opts.OwnerID,
		OrganizationID:    opts.OrganizationID,
		ProjectID:         opts.ProjectID,
		LastModelConfigID: opts.ModelConfigID,
	}
	var types []customAgentType
	if p.agentDefinitionsEnabled() {
		types = mergeCustomAgentTypes(nil, p.storedAgentTypes(ctx, chat))
	}
	agentType, ok := lookupCustomAgentType(types, id)
	if !ok {
		ids := []string{subagentTypeGeneral, subagentTypeExplore}
		for _, t := range types {
			ids = append(ids, t.Alias)
		}
		return RootAgent{}, &RootAgentError{Reason: fmt.Sprintf("type must be one of: %s (%s)", strings.Join(ids, ", "), rootAgentWorkspaceReason)}
	}
	def, err := agentType.load(ctx)
	if err != nil {
		return RootAgent{}, &RootAgentError{Reason: err.Error()}
	}
	agent := RootAgent{Name: def.Name, prompt: def.Prompt}
	if def.BaseType == workspacesdk.AgentDefinitionBaseExplore {
		agent.mode = database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true}
	}
	agent.noOrchestration = !keepsOrchestrationTools(agent.mode, &def)
	if !opts.ExplicitModel && def.Model != "" {
		modelConfigID, err := p.resolveCustomAgentModel(ctx, chat, def)
		if err != nil {
			return RootAgent{}, &RootAgentError{Reason: err.Error()}
		}
		agent.ModelConfigID = uuid.NullUUID{UUID: modelConfigID, Valid: true}
	}
	agent.ReasoningEffort, err = customAgentReasoningEffort(def)
	if err != nil {
		return RootAgent{}, &RootAgentError{Reason: err.Error()}
	}
	raw, err := json.Marshal(customAgentToolPolicyFromDefinition(def))
	if err != nil {
		return RootAgent{}, xerrors.Errorf("marshal tool policy of agent %q: %w", def.Name, err)
	}
	agent.toolPolicy = pqtype.NullRawMessage{RawMessage: raw, Valid: true}
	return agent, nil
}

// withRootAgent returns opts with the mode and prompt of its root agent. The
// definition's prompt follows the caller's system prompt, so the deployment
// and organization prompts stay first.
func withRootAgent(opts CreateOptions) CreateOptions {
	agent := opts.RootAgent
	if agent == nil {
		return opts
	}
	if agent.mode.Valid {
		opts.ChatMode = agent.mode
	}
	if prompt := strings.TrimSpace(agent.prompt); prompt != "" {
		opts.SystemPrompt = strings.TrimSpace(opts.SystemPrompt + "\n\n" + prompt)
	}
	return opts
}

// deploymentPrompt returns prompt without the orchestration guidance when
// agent cannot spawn. A nil agent keeps prompt.
func (agent *RootAgent) deploymentPrompt(prompt string) string {
	if agent == nil || !agent.noOrchestration {
		return prompt
	}
	return strings.Replace(prompt, subagentOrchestrationPromptBlock, "", 1)
}

// rootAgentAfterInsert stores the chat_subagents row of a root chat that
// starts as agent. Depth 0 keeps the root out of every subagent count, which
// all select children by parent_chat_id or root_chat_id.
func rootAgentAfterInsert(agent *RootAgent) func(context.Context, database.Store, database.Chat) error {
	if agent == nil {
		return nil
	}
	return func(ctx context.Context, store database.Store, chat database.Chat) error {
		ftx, err := forkdb.From(store)
		if err != nil {
			return xerrors.Errorf("fork queries: %w", err)
		}
		if _, err := ftx.InsertChatSubagent(ctx, forkdb.InsertChatSubagentParams{
			ChatID:     chat.ID,
			AgentType:  agent.Name,
			Isolation:  subagentIsolationShared,
			ToolPolicy: agent.toolPolicy,
		}); err != nil {
			return xerrors.Errorf("insert root agent type: %w", err)
		}
		return nil
	}
}

// chainAfterInsert runs the non-nil hooks in order.
func chainAfterInsert(hooks ...func(context.Context, database.Store, database.Chat) error) func(context.Context, database.Store, database.Chat) error {
	return func(ctx context.Context, store database.Store, chat database.Chat) error {
		for _, hook := range hooks {
			if hook == nil {
				continue
			}
			if err := hook(ctx, store, chat); err != nil {
				return err
			}
		}
		return nil
	}
}
