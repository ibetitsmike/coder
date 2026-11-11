package chatd

// This file is fork code: it resolves custom agent types for spawn_agent from
// the chat's pinned subagent context resources and the stored personal,
// organization and project agent definitions behind the
// fork-agent-definitions experiment. Upstream's subagent_catalog.go calls
// forkSubagentDefinitions and forkLookupSubagentDefinition, which return only
// the built-in types when the experiment is off.

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	skillspkg "github.com/coder/coder/v2/coderd/x/skills"
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
// Built-in types win over custom types with the same name. A custom type
// matches its bare name when only one tier defines it, and always matches
// its tier-qualified name, such as personal/reviewer.
func forkLookupSubagentDefinition(ctx context.Context, p *Server, chat database.Chat, id string) (subagentDefinition, bool) {
	if def, ok := lookupSubagentDefinition(id); ok {
		if def.id == subagentTypeComputerUse && p.agentDefinitionsEnabled() {
			def = computerUseDefinitionWithExplicitModel(def)
		}
		return def, true
	}
	agentType, ok := lookupCustomAgentType(customAgentTypes(ctx, p, chat), id)
	if !ok {
		return subagentDefinition{}, false
	}
	return customSubagentDefinition(agentType, agentType.Description), true
}

// lookupCustomAgentType returns the type in types that id names, by its bare
// name when only one tier defines it or by its tier-qualified name.
func lookupCustomAgentType(types []customAgentType, id string) (customAgentType, bool) {
	resolved := make([]skillspkg.ResolvedSkill, 0, len(types))
	for _, agentType := range types {
		resolved = append(resolved, agentType.ResolvedSkill)
	}
	match, err := skillspkg.Lookup(resolved, id)
	if err != nil {
		return customAgentType{}, false
	}
	for _, agentType := range types {
		if agentType.ResolvedSkill == match {
			return agentType, true
		}
	}
	return customAgentType{}, false
}

// customAgentType is a custom agent type from one tier. load reads and
// parses its definition; stored tiers read it only when the type spawns.
type customAgentType struct {
	skillspkg.ResolvedSkill
	load func(context.Context) (workspacesdk.AgentDefinition, error)
}

// customSubagentDefinitions returns the chat's custom agent types sorted by
// name, with the description budget applied.
func customSubagentDefinitions(ctx context.Context, p *Server, chat database.Chat) []subagentDefinition {
	types := customAgentTypes(ctx, p, chat)
	descriptions := make([]string, 0, len(types))
	for _, agentType := range types {
		descriptions = append(descriptions, agentType.Description)
	}
	descriptions = budgetCustomAgentDescriptions(descriptions)
	out := make([]subagentDefinition, 0, len(types))
	for i, agentType := range types {
		out = append(out, customSubagentDefinition(agentType, descriptions[i]))
	}
	return out
}

// customAgentTypes merges the custom agent types pinned to the chat's
// workspace with the chat owner's personal types, the organization types
// shared with the owner and the types of the chat's project, using the skill
// alias rules: a name defined in several tiers is only reachable by its
// tier-qualified name. Built-in type names are reserved in every tier. Custom
// types are optional, so read and parse failures drop types instead of
// failing the turn.
func customAgentTypes(ctx context.Context, p *Server, chat database.Chat) []customAgentType {
	if !p.agentDefinitionsEnabled() || chat.ID == uuid.Nil {
		return nil
	}
	return mergeCustomAgentTypes(workspaceAgentDefinitions(ctx, p, chat), p.storedAgentTypes(ctx, chat))
}

// mergeCustomAgentTypes merges the workspace definitions with the stored
// tiers, as customAgentTypes describes.
func mergeCustomAgentTypes(workspace []workspacesdk.AgentDefinition, stored []tierAgentType) []customAgentType {
	tiers := map[skillspkg.Source][]skillspkg.Skill{}
	loaders := map[string]func(context.Context) (workspacesdk.AgentDefinition, error){}
	add := func(agentType tierAgentType) {
		alias := skillspkg.QualifiedAlias(agentType.source, agentType.name)
		if _, builtin := lookupSubagentDefinition(agentType.name); builtin || loaders[alias] != nil {
			return
		}
		loaders[alias] = agentType.load
		tiers[agentType.source] = append(tiers[agentType.source], skillspkg.Skill{Name: agentType.name, Description: agentType.description})
	}
	for _, def := range workspace {
		add(tierAgentType{
			source:      skillspkg.SourceWorkspace,
			name:        def.Name,
			description: def.Description,
			load:        func(context.Context) (workspacesdk.AgentDefinition, error) { return def, nil },
		})
	}
	for _, agentType := range stored {
		add(agentType)
	}

	merged := skillspkg.MergeSkills(
		skillspkg.SourceSkills{Source: skillspkg.SourcePersonal, Skills: tiers[skillspkg.SourcePersonal]},
		skillspkg.SourceSkills{Source: skillspkg.SourceOrganization, Skills: tiers[skillspkg.SourceOrganization]},
		skillspkg.SourceSkills{Source: skillspkg.SourceProject, Skills: tiers[skillspkg.SourceProject]},
		skillspkg.SourceSkills{Source: skillspkg.SourceWorkspace, Skills: tiers[skillspkg.SourceWorkspace]},
	)
	out := make([]customAgentType, 0, len(merged))
	for _, resolved := range merged {
		out = append(out, customAgentType{
			ResolvedSkill: resolved,
			load:          loaders[skillspkg.QualifiedAlias(resolved.Source, resolved.Name)],
		})
	}
	return out
}

// workspaceAgentDefinitions parses the agent definitions pinned to chat
// from its workspace.
func workspaceAgentDefinitions(ctx context.Context, p *Server, chat database.Chat) []workspacesdk.AgentDefinition {
	resources, err := p.db.ListChatContextResourcesByChatID(ctx, chat.ID)
	if err != nil {
		p.logger.Warn(ctx, "failed to list chat context resources for custom agent types",
			slog.F("chat_id", chat.ID), slog.Error(err))
		return nil
	}
	return parseAgentDefinitionResources(resources)
}

// parseAgentDefinitionResources parses the OK agent definition files in
// resources and skips the rest.
func parseAgentDefinitionResources(resources []database.ChatContextResource) []workspacesdk.AgentDefinition {
	var defs []workspacesdk.AgentDefinition
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
		defs = append(defs, def)
	}
	return defs
}

// tierAgentType is a custom agent type from one tier, before the tiers
// are merged.
type tierAgentType struct {
	source      skillspkg.Source
	name        string
	description string
	load        func(context.Context) (workspacesdk.AgentDefinition, error)
}

// storedAgentTypes returns the chat owner's enabled personal agent
// definitions in the chat's organization, the enabled organization agent
// definitions shared with the owner, and the enabled agent definitions of
// the chat's project. Personal and organization reads run as the owner, so
// dbauthz applies organization membership and the definitions' ACLs.
// Project reads run as chatd once the owner passes the usable-project check,
// like project skills, so role grants do not expose a project's types.
func (p *Server) storedAgentTypes(ctx context.Context, chat database.Chat) []tierAgentType {
	logger := p.logger.With(slog.F("chat_id", chat.ID))
	fdb, err := forkdb.From(p.db)
	if err != nil {
		logger.Warn(ctx, "fork queries unavailable for stored agent definitions", slog.Error(err))
		return nil
	}
	owner, err := p.chatOwnerSubject(ctx, chat.OwnerID)
	if err != nil {
		logger.Warn(ctx, "failed to load chat owner for stored agent definitions", slog.Error(err))
		return nil
	}
	asOwner := func(ctx context.Context) context.Context {
		//nolint:gocritic // Stored agent definition ACLs are evaluated against the chat owner.
		return dbauthz.As(ctx, owner)
	}

	var out []tierAgentType
	personal, err := fdb.ListUserAgentDefinitionMetadata(asOwner(ctx), forkdb.ListUserAgentDefinitionMetadataParams{
		OrganizationID: chat.OrganizationID,
		UserID:         chat.OwnerID,
	})
	if err != nil {
		logger.Warn(ctx, "failed to list personal agent definitions", slog.Error(err))
	}
	for _, row := range personal {
		if !row.Enabled {
			continue
		}
		out = append(out, tierAgentType{
			source:      skillspkg.SourcePersonal,
			name:        row.Name,
			description: row.Description,
			load: func(ctx context.Context) (workspacesdk.AgentDefinition, error) {
				stored, err := fdb.GetUserAgentDefinitionByName(asOwner(ctx), forkdb.GetUserAgentDefinitionByNameParams{
					OrganizationID: chat.OrganizationID,
					UserID:         chat.OwnerID,
					Name:           row.Name,
				})
				if err != nil {
					return workspacesdk.AgentDefinition{}, xerrors.Errorf("load agent %q: %w", row.Name, err)
				}
				return parseStoredAgentDefinition(stored)
			},
		})
	}
	organization, err := fdb.ListOrganizationAgentDefinitionMetadata(asOwner(ctx), chat.OrganizationID)
	if err != nil {
		logger.Warn(ctx, "failed to list organization agent definitions", slog.Error(err))
	}
	for _, row := range organization {
		if !row.Enabled {
			continue
		}
		out = append(out, tierAgentType{
			source:      skillspkg.SourceOrganization,
			name:        row.Name,
			description: row.Description,
			load: func(ctx context.Context) (workspacesdk.AgentDefinition, error) {
				stored, err := fdb.GetOrganizationAgentDefinitionByName(asOwner(ctx), forkdb.GetOrganizationAgentDefinitionByNameParams{
					OrganizationID: chat.OrganizationID,
					Name:           row.Name,
				})
				if err != nil {
					return workspacesdk.AgentDefinition{}, xerrors.Errorf("load agent %q: %w", row.Name, err)
				}
				return parseStoredAgentDefinition(stored)
			},
		})
	}
	projectID := p.resolveUsableChatProjectID(ctx, logger, chat)
	if !projectID.Valid {
		return out
	}
	project, err := fdb.ListProjectAgentDefinitionMetadata(ctx, projectID.UUID)
	if err != nil {
		logger.Warn(ctx, "failed to list project agent definitions", slog.F("project_id", projectID.UUID), slog.Error(err))
	}
	for _, row := range project {
		if !row.Enabled {
			continue
		}
		out = append(out, tierAgentType{
			source:      skillspkg.SourceProject,
			name:        row.Name,
			description: row.Description,
			load: func(ctx context.Context) (workspacesdk.AgentDefinition, error) {
				stored, err := fdb.GetProjectAgentDefinitionByName(ctx, forkdb.GetProjectAgentDefinitionByNameParams{
					ProjectID: projectID.UUID,
					Name:      row.Name,
				})
				if err != nil {
					return workspacesdk.AgentDefinition{}, xerrors.Errorf("load agent %q: %w", row.Name, err)
				}
				return parseStoredAgentDefinition(stored)
			},
		})
	}
	return out
}

// parseStoredAgentDefinition parses a stored agent definition read at spawn
// time. The row may have been disabled since the catalog listed it, and its
// content must keep the row's name, which the catalog checked against the
// built-in types.
func parseStoredAgentDefinition(row forkdb.AgentDefinition) (workspacesdk.AgentDefinition, error) {
	if !row.Enabled {
		return workspacesdk.AgentDefinition{}, xerrors.Errorf("agent %q is disabled", row.Name)
	}
	def, err := workspacesdk.ParseAgentDefinition(row.Content, row.Name)
	if err != nil {
		return workspacesdk.AgentDefinition{}, xerrors.Errorf("parse agent %q: %w", row.Name, err)
	}
	if def.Name != row.Name {
		return workspacesdk.AgentDefinition{}, xerrors.Errorf("agent %q content names %q", row.Name, def.Name)
	}
	return def, nil
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

func customSubagentDefinition(agentType customAgentType, description string) subagentDefinition {
	return subagentDefinition{
		id:          agentType.Alias,
		description: description,
		buildOptions: func(ctx context.Context, p *Server, parent database.Chat, turnParent database.Chat, currentModelConfigID uuid.UUID, explicitModelConfigID *uuid.UUID, prompt string) (childSubagentChatOptions, error) {
			def, err := agentType.load(ctx)
			if err != nil {
				return childSubagentChatOptions{}, err
			}
			base, ok := lookupSubagentDefinition(def.BaseType)
			if !ok {
				return childSubagentChatOptions{}, xerrors.Errorf("agent %q has unknown base type %q", def.Name, def.BaseType)
			}
			// A definition without a model inherits the parent's model
			// rather than the base type's subagent model override.
			if explicitModelConfigID == nil {
				modelConfigID := currentModelConfigID
				if def.Model != "" {
					modelConfigID, err = p.resolveCustomAgentModel(ctx, parent, def)
					if err != nil {
						return childSubagentChatOptions{}, err
					}
				}
				explicitModelConfigID = &modelConfigID
			}
			options, err := base.buildOptions(ctx, p, parent, turnParent, currentModelConfigID, explicitModelConfigID, prompt)
			if err != nil {
				return childSubagentChatOptions{}, err
			}
			// spawnAgent applies an explicit reasoning_effort after
			// buildOptions, so it still wins over the definition's.
			effort, err := customAgentReasoningEffort(def)
			if err != nil {
				return childSubagentChatOptions{}, err
			}
			if effort != "" {
				options.reasoningEffortOverride = &effort
			}
			options.systemPrompt = def.Prompt
			options.customAgent = &def
			return options, nil
		},
	}
}

// customAgentReasoningEffort returns the definition's reasoning effort in
// lower case, or "" when it sets none.
func customAgentReasoningEffort(def workspacesdk.AgentDefinition) (string, error) {
	effort := strings.ToLower(def.ReasoningEffort)
	if effort != "" && !chatprovider.IsValidReasoningEffort(effort) {
		return "", xerrors.Errorf(
			"agent %q sets reasoning_effort %q, which is not one of %s",
			def.Name, def.ReasoningEffort,
			strings.Join(codersdk.ChatModelReasoningEffortValues(), ", "),
		)
	}
	return effort, nil
}

// resolveCustomAgentModel matches the definition's model against the models
// the chat owner can spawn, by model config ID, then model, then display
// name, ignoring case, then alias.
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
	return p.resolveCustomAgentModelAlias(ctx, parent, def, models)
}

// subagentContextResource reports an OK pinned agent definition file on the
// chat, keeping its parse warnings in Error. ok is false when the body has no
// name, like an OK skill without one.
func subagentContextResource(r database.ChatContextResource) (codersdk.ChatContextResource, bool) {
	var body agentproto.SubagentBody
	if err := contextBodyUnmarshalOptions.Unmarshal(r.Body, &body); err != nil || body.GetName() == "" {
		return codersdk.ChatContextResource{}, false
	}
	return codersdk.ChatContextResource{
		Source:              r.Source,
		Kind:                codersdk.ChatContextResourceKindSubagent,
		SizeBytes:           r.SizeBytes,
		Status:              codersdk.ChatContextResourceStatusOK,
		Error:               r.Error,
		SubagentName:        body.GetName(),
		SubagentDescription: body.GetDescription(),
	}, true
}
