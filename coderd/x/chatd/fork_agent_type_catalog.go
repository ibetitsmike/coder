package chatd

// This file is fork code: it lists the agent types of the
// fork-agent-definitions experiment for the agent type catalog route.

import (
	"cmp"
	"context"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	skillspkg "github.com/coder/coder/v2/coderd/x/skills"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// AgentTypeCatalogOptions selects whose agent types AgentTypeCatalog lists.
type AgentTypeCatalogOptions struct {
	OwnerID        uuid.UUID
	OrganizationID uuid.UUID
	ProjectID      uuid.NullUUID
	// WorkspaceAgentID adds the agent definitions that this workspace agent
	// reports. The caller in ctx must be able to read its workspace.
	WorkspaceAgentID uuid.NullUUID
}

// AgentTypeCatalog lists the agent types that spawn_agent offers a chat of
// opts.OwnerID in opts, named as spawn_agent and ResolveRootAgent accept
// them, and reports whether a new root chat can start as each.
func (p *Server) AgentTypeCatalog(ctx context.Context, opts AgentTypeCatalogOptions) ([]codersdk.ForkAgentType, error) {
	var out []codersdk.ForkAgentType
	for _, id := range []string{subagentTypeGeneral, subagentTypeExplore} {
		def, _ := lookupSubagentDefinition(id)
		out = append(out, codersdk.ForkAgentType{
			Name: id, Source: codersdk.ForkAgentTypeSourceBuiltin, Description: def.description, BaseType: id, RootAvailable: true,
		})
	}
	if !p.agentDefinitionsEnabled() {
		return out, nil
	}
	var workspace []workspacesdk.AgentDefinition
	if opts.WorkspaceAgentID.Valid {
		resources, err := p.db.ListWorkspaceAgentContextResources(ctx, opts.WorkspaceAgentID.UUID)
		if err != nil {
			return nil, xerrors.Errorf("list workspace agent context resources: %w", err)
		}
		chatResources := make([]database.ChatContextResource, 0, len(resources))
		for _, resource := range resources {
			chatResources = append(chatResources, database.ChatContextResource{
				BodyKind: resource.BodyKind, Body: resource.Body, Status: resource.Status,
			})
		}
		workspace = parseAgentDefinitionResources(chatResources)
	}
	chat := database.Chat{ID: uuid.New(), OwnerID: opts.OwnerID, OrganizationID: opts.OrganizationID, ProjectID: opts.ProjectID}
	for _, agentType := range mergeCustomAgentTypes(workspace, p.storedAgentTypes(ctx, chat)) {
		entry := codersdk.ForkAgentType{
			Name:        agentType.Alias,
			Source:      string(agentType.Source),
			Description: agentType.Description,
			BaseType:    workspacesdk.AgentDefinitionBaseGeneral,
		}
		def, err := agentType.load(ctx)
		switch {
		case err != nil:
			entry.RootUnavailableReason = err.Error()
		case agentType.Source == skillspkg.SourceWorkspace:
			entry.RootUnavailableReason = rootAgentWorkspaceReason
		default:
			if _, err := p.rootAgentFromDefinition(ctx, chat, def, false); err != nil {
				entry.RootUnavailableReason = err.Error()
			}
		}
		if err == nil {
			entry.Model = def.Model
			entry.BaseType = cmp.Or(def.BaseType, entry.BaseType)
		}
		entry.RootAvailable = entry.RootUnavailableReason == ""
		out = append(out, entry)
	}
	return out, nil
}
