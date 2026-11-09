package chatd

// This file is fork code: it resolves custom agent types for spawn_agent from
// the chat's pinned subagent context resources behind the
// fork-agent-definitions experiment. Upstream's subagent_catalog.go calls
// forkSubagentDefinitions and forkLookupSubagentDefinition, which return only
// the built-in types when the experiment is off.

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	// customAgentDescriptionMaxBytes and customAgentDescriptionsMaxBytes
	// bound the spawn_agent tool description, which is sent on every turn.
	customAgentDescriptionMaxBytes  = 256
	customAgentDescriptionsMaxBytes = 2048
	customAgentFallbackDescription  = "custom agent"
)

func (p *Server) agentDefinitionsEnabled() bool {
	return p.experiments.Enabled(codersdk.ExperimentForkAgentDefinitions)
}

// forkSubagentDefinitions returns the built-in subagent types followed by the
// chat's custom agent types.
func forkSubagentDefinitions(ctx context.Context, p *Server, chat database.Chat) []subagentDefinition {
	return append(allSubagentDefinitions(), customSubagentDefinitions(ctx, p, chat)...)
}

// forkLookupSubagentDefinition returns the subagent type with the given id.
// Built-in types win over custom types with the same name.
func forkLookupSubagentDefinition(ctx context.Context, p *Server, chat database.Chat, id string) (subagentDefinition, bool) {
	if def, ok := lookupSubagentDefinition(id); ok {
		return def, true
	}
	for _, def := range customSubagentDefinitions(ctx, p, chat) {
		if def.id == id {
			return def, true
		}
	}
	return subagentDefinition{}, false
}

// customSubagentDefinitions returns the custom agent types pinned to chat,
// sorted by name. Custom types are optional, so read and parse failures
// drop types instead of failing the turn.
func customSubagentDefinitions(ctx context.Context, p *Server, chat database.Chat) []subagentDefinition {
	if !p.agentDefinitionsEnabled() || chat.ID == uuid.Nil {
		return nil
	}
	resources, err := p.db.ListChatContextResourcesByChatID(ctx, chat.ID)
	if err != nil {
		p.logger.Warn(ctx, "failed to list chat context resources for custom agent types",
			slog.F("chat_id", chat.ID), slog.Error(err))
		return nil
	}
	var defs []workspacesdk.AgentDefinition
	seen := map[string]bool{}
	for _, resource := range resources {
		if resource.BodyKind != database.WorkspaceAgentContextBodyKindSubagent ||
			resource.Status != database.WorkspaceAgentContextResourceStatusOk {
			continue
		}
		var body agentproto.SubagentBody
		if err := contextBodyUnmarshalOptions.Unmarshal(resource.Body, &body); err != nil {
			continue
		}
		def, err := workspacesdk.ParseAgentDefinition(string(body.GetDefinition()), body.GetName())
		if err != nil {
			continue
		}
		if _, builtin := lookupSubagentDefinition(def.Name); builtin || seen[def.Name] {
			continue
		}
		seen[def.Name] = true
		defs = append(defs, def)
	}
	slices.SortFunc(defs, func(a, b workspacesdk.AgentDefinition) int {
		return strings.Compare(a.Name, b.Name)
	})

	descriptions := make([]string, 0, len(defs))
	for _, def := range defs {
		descriptions = append(descriptions, def.Description)
	}
	descriptions = budgetCustomAgentDescriptions(descriptions)
	out := make([]subagentDefinition, 0, len(defs))
	for i, def := range defs {
		out = append(out, customSubagentDefinition(def, descriptions[i]))
	}
	return out
}

// budgetCustomAgentDescriptions collapses whitespace in each description,
// truncates it to customAgentDescriptionMaxBytes, and replaces it with
// customAgentFallbackDescription when empty or once the total would exceed
// customAgentDescriptionsMaxBytes.
func budgetCustomAgentDescriptions(descriptions []string) []string {
	out := make([]string, 0, len(descriptions))
	total := 0
	exhausted := false
	for _, description := range descriptions {
		description = strings.Join(strings.Fields(description), " ")
		if len(description) > customAgentDescriptionMaxBytes {
			cut := customAgentDescriptionMaxBytes - len("...")
			for cut > 0 && !utf8.RuneStart(description[cut]) {
				cut--
			}
			description = description[:cut] + "..."
		}
		if !exhausted && total+len(description) > customAgentDescriptionsMaxBytes {
			exhausted = true
		}
		if exhausted || description == "" {
			out = append(out, customAgentFallbackDescription)
			continue
		}
		total += len(description)
		out = append(out, description)
	}
	return out
}

func customSubagentDefinition(def workspacesdk.AgentDefinition, description string) subagentDefinition {
	return subagentDefinition{
		id:          def.Name,
		description: description,
		buildOptions: func(ctx context.Context, p *Server, parent database.Chat, turnParent database.Chat, currentModelConfigID uuid.UUID, explicitModelConfigID *uuid.UUID, prompt string) (childSubagentChatOptions, error) {
			base, ok := lookupSubagentDefinition(def.BaseType)
			if !ok {
				return childSubagentChatOptions{}, xerrors.Errorf("agent %q has unknown base type %q", def.Name, def.BaseType)
			}
			if explicitModelConfigID == nil && def.Model != "" {
				modelConfigID, err := p.resolveCustomAgentModel(ctx, parent, def)
				if err != nil {
					return childSubagentChatOptions{}, err
				}
				explicitModelConfigID = &modelConfigID
			}
			options, err := base.buildOptions(ctx, p, parent, turnParent, currentModelConfigID, explicitModelConfigID, prompt)
			if err != nil {
				return childSubagentChatOptions{}, err
			}
			if def.ReasoningEffort != "" {
				effort := strings.ToLower(def.ReasoningEffort)
				if !chatprovider.IsValidReasoningEffort(effort) {
					return childSubagentChatOptions{}, xerrors.Errorf(
						"agent %q sets reasoning_effort %q, which is not one of %s",
						def.Name, def.ReasoningEffort,
						strings.Join(codersdk.ChatModelReasoningEffortValues(), ", "),
					)
				}
				options.reasoningEffortOverride = &effort
			}
			options.systemPrompt = def.Prompt
			options.agentType = def.Name
			return options, nil
		},
	}
}

// resolveCustomAgentModel matches the definition's model against the models
// the chat owner can spawn, by model config ID, then model, then display
// name, ignoring case.
func (p *Server) resolveCustomAgentModel(ctx context.Context, parent database.Chat, def workspacesdk.AgentDefinition) (uuid.UUID, error) {
	models, err := p.listSpawnableModelConfigs(ctx, parent.OwnerID, parent.OrganizationID)
	if err != nil {
		return uuid.Nil, xerrors.Errorf("list spawnable model configs: %w", err)
	}
	for _, key := range []string{"model_config_id", "model", "display_name"} {
		for _, model := range models {
			value, _ := model[key].(string)
			if !strings.EqualFold(value, def.Model) {
				continue
			}
			id, _ := model["model_config_id"].(string)
			modelConfigID, err := uuid.Parse(id)
			if err != nil {
				return uuid.Nil, xerrors.Errorf("parse model config id %q: %w", id, err)
			}
			return modelConfigID, nil
		}
	}
	return uuid.Nil, xerrors.Errorf(
		"agent %q sets model %q, which is not an available model; pass model_config_id from %s instead",
		def.Name, def.Model, listSubagentModelsToolName,
	)
}
