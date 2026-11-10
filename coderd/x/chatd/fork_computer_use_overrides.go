package chatd

// This file is fork code: with the fork-agent-definitions experiment on,
// spawn_agent's model_config_id and reasoning_effort also apply to type
// computer_use. Upstream's spawnAgent rejects both for computer_use only when
// the experiment is off, forkLookupSubagentDefinition swaps in
// computerUseDefinitionWithExplicitModel, and appendForkTools rewrites the
// spawn_agent text that says they are unsupported.

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

const (
	upstreamSpawnAgentOverridesSentence = "both apply only to type \"" + subagentTypeGeneral + "\" and type \"" + subagentTypeExplore + "\". "
	upstreamSpawnAgentArgUnsupported    = " Not supported for type 'computer_use'."
)

// computerUseDefinitionWithExplicitModel returns def, the built-in
// computer_use type, with buildOptions running the child on an explicit
// model_config_id when that model supports computer use. The explicit model
// replaces the organization's computer_use override, so the override's
// reasoning effort is dropped with it.
func computerUseDefinitionWithExplicitModel(def subagentDefinition) subagentDefinition {
	build := def.buildOptions
	def.buildOptions = func(ctx context.Context, p *Server, parent database.Chat, turnParent database.Chat, currentModelConfigID uuid.UUID, explicitModelConfigID *uuid.UUID, prompt string) (childSubagentChatOptions, error) {
		options, err := build(ctx, p, parent, turnParent, currentModelConfigID, explicitModelConfigID, prompt)
		if err != nil || explicitModelConfigID == nil {
			return options, err
		}
		if err := p.requireComputerUseModel(ctx, *explicitModelConfigID); err != nil {
			return childSubagentChatOptions{}, err
		}
		options.modelConfigIDOverride = explicitModelConfigID
		options.reasoningEffortOverride = nil
		return options, nil
	}
	return def
}

// requireComputerUseModel returns an error unless the model config can run a
// computer use child. resolveExplicitSpawnOverrides has already checked that
// the chat owner can use it.
func (p *Server) requireComputerUseModel(ctx context.Context, modelConfigID uuid.UUID) error {
	//nolint:gocritic // Chatd reads model configuration for the chat owner.
	chatdCtx := dbauthz.AsChatd(ctx)
	config, err := p.db.GetChatModelConfigByID(chatdCtx, modelConfigID)
	if errors.Is(err, sql.ErrNoRows) {
		return xerrors.New("model_config_id not found or is disabled; use " + listSubagentModelsToolName + " to see available models")
	}
	if err != nil {
		return xerrors.Errorf("get model config %s: %w", modelConfigID, err)
	}
	aiProvider, err := p.db.GetAIProviderByID(chatdCtx, config.AIProviderID.UUID)
	if err != nil {
		return xerrors.Errorf("get AI provider for model %s: %w", config.ID, err)
	}
	provider, ok := chatprovider.ComputerUseProvider(aiProvider.Type, config.Model)
	if !ok {
		return xerrors.Errorf(
			`model_config_id model %s uses AI provider %q of type %q, which cannot run type %q; computer use requires %s`,
			computerUseModelLabel(config), aiProvider.Name, aiProvider.Type, subagentTypeComputerUse, chatprovider.ComputerUseModelRequirement,
		)
	}
	if provider == codersdk.ChatComputerUseProviderOpenAI {
		// The child's first turn would fail in requireResponsesAPI otherwise.
		callConfig, err := parseModelConfigOptions(config.Options)
		if err != nil {
			return xerrors.Errorf("parse options for model %s: %w", config.ID, err)
		}
		if _, err := requireResponsesAPI(callConfig.OpenAIConfig, config.Model); err != nil {
			return xerrors.Errorf("model_config_id: %w", err)
		}
	}
	return nil
}

// computerUseOverridesSpawnTool is spawn_agent with its text saying that
// model_config_id and reasoning_effort apply to computer_use too.
type computerUseOverridesSpawnTool struct {
	fantasy.AgentTool
}

// withComputerUseSpawnOverrides wraps the spawn_agent tool in tools, if there
// is one, with computerUseOverridesSpawnTool.
func (p *Server) withComputerUseSpawnOverrides(tools []fantasy.AgentTool) []fantasy.AgentTool {
	if !p.agentDefinitionsEnabled() {
		return tools
	}
	for i, tool := range tools {
		if tool.Info().Name == spawnAgentToolName {
			tools[i] = computerUseOverridesSpawnTool{AgentTool: tool}
		}
	}
	return tools
}

func (t computerUseOverridesSpawnTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description = strings.Replace(info.Description, upstreamSpawnAgentOverridesSentence,
		"both apply to every type. A computer_use model_config_id must support computer use. ", 1)
	info.Parameters = maps.Clone(info.Parameters)
	for _, name := range []string{"model_config_id", "reasoning_effort"} {
		property, ok := info.Parameters[name].(map[string]any)
		if !ok {
			continue
		}
		property = maps.Clone(property)
		if description, ok := property["description"].(string); ok {
			property["description"] = strings.Replace(description, upstreamSpawnAgentArgUnsupported, "", 1)
		}
		info.Parameters[name] = property
	}
	return info
}
