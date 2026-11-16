package forkdb

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/rbac"
)

func (p Plugin) RBACObject() rbac.Object {
	return rbac.ResourcePlugin.WithID(p.ID).InOrg(p.OrganizationID)
}

// RBACObject reuses the organization skill object, so only organization
// admins and auditors read packs.
func (p PluginPack) RBACObject() rbac.Object {
	return rbac.ResourceOrganizationSkill.WithID(p.ID).InOrg(p.OrganizationID)
}

// RBACObject reuses the skill objects. A user-tier row has no InOrg, like a
// personal skill, because policy.rego skips user permissions on objects with
// an organization owner. dbauthz checks the organization membership instead.
func (d AgentDefinition) RBACObject() rbac.Object {
	if d.UserID.Valid {
		return rbac.ResourceUserSkill.WithID(d.ID).WithOwner(d.UserID.UUID.String())
	}
	if !d.ProjectID.Valid {
		return organizationAgentDefinitionObject(d.ID, d.OrganizationID, d.GroupAcl, d.UserAcl)
	}
	// No role grants the empty resource type, so project-tier rows stay
	// unauthorizable until the project tier gets its own checks.
	return rbac.Object{ID: d.ID.String()}
}

func (r ListOrganizationAgentDefinitionMetadataRow) RBACObject() rbac.Object {
	return organizationAgentDefinitionObject(r.ID, r.OrganizationID, r.GroupAcl, r.UserAcl)
}

// RBACObject reuses the skill objects, like AgentDefinition.RBACObject.
func (w SavedWorkflow) RBACObject() rbac.Object {
	if w.UserID.Valid {
		return rbac.ResourceUserSkill.WithID(w.ID).WithOwner(w.UserID.UUID.String())
	}
	return organizationAgentDefinitionObject(w.ID, w.OrganizationID, w.GroupAcl, w.UserAcl)
}

func (r ListOrganizationSavedWorkflowMetadataRow) RBACObject() rbac.Object {
	return organizationAgentDefinitionObject(r.ID, r.OrganizationID, r.GroupAcl, r.UserAcl)
}

func organizationAgentDefinitionObject(id, organizationID uuid.UUID, groupACL, userACL database.ChatACL) rbac.Object {
	return rbac.ResourceOrganizationSkill.
		WithID(id).
		InOrg(organizationID).
		WithGroupACL(groupACL.RBACACL()).
		WithACLUserList(userACL.RBACACL())
}

// Secret reports whether any item of the question asks for a secret answer,
// which only the secret-answer route accepts. Undecodable questions count as
// secret, so a bad row never opens the normal answer path to a secret.
func (q ChatDescendantQuestion) Secret() bool {
	var items []struct {
		Secret bool `json:"secret"`
	}
	if err := json.Unmarshal(q.Questions, &items); err != nil {
		return true
	}
	for _, item := range items {
		if item.Secret {
			return true
		}
	}
	return false
}

// Chat returns c as upstream's database.Chat. TestChatsExpandedChat fails
// when a column of chats_expanded is missing on either side.
func (c ChatsExpanded) Chat() database.Chat {
	return database.Chat{
		ID:                       c.ID,
		OwnerID:                  c.OwnerID,
		WorkspaceID:              c.WorkspaceID,
		Title:                    c.Title,
		Status:                   c.Status,
		WorkerID:                 c.WorkerID,
		StartedAt:                c.StartedAt,
		HeartbeatAt:              c.HeartbeatAt,
		CreatedAt:                c.CreatedAt,
		UpdatedAt:                c.UpdatedAt,
		ParentChatID:             c.ParentChatID,
		RootChatID:               c.RootChatID,
		LastModelConfigID:        c.LastModelConfigID,
		LastReasoningEffort:      c.LastReasoningEffort,
		Archived:                 c.Archived,
		LastError:                c.LastError,
		Mode:                     c.Mode,
		MCPServerIDs:             c.McpServerIds,
		Labels:                   c.Labels,
		BuildID:                  c.BuildID,
		AgentID:                  c.AgentID,
		PinOrder:                 c.PinOrder,
		LastReadMessageID:        c.LastReadMessageID,
		DynamicTools:             c.DynamicTools,
		OrganizationID:           c.OrganizationID,
		ProjectID:                c.ProjectID,
		PlanMode:                 c.PlanMode,
		ClientType:               c.ClientType,
		LastTurnSummary:          c.LastTurnSummary,
		Summary:                  c.Summary,
		SummaryGeneratedAt:       c.SummaryGeneratedAt,
		SnapshotVersion:          c.SnapshotVersion,
		HistoryVersion:           c.HistoryVersion,
		QueueVersion:             c.QueueVersion,
		GenerationAttempt:        c.GenerationAttempt,
		RetryState:               c.RetryState,
		RetryStateVersion:        c.RetryStateVersion,
		RunnerID:                 c.RunnerID,
		RequiresActionDeadlineAt: c.RequiresActionDeadlineAt,
		UserACL:                  c.UserAcl,
		GroupACL:                 c.GroupAcl,
		OwnerUsername:            c.OwnerUsername,
		OwnerName:                c.OwnerName,
		ContextAggregateHash:     c.ContextAggregateHash,
		ContextDirtySince:        c.ContextDirtySince,
		ContextDirtyResources:    c.ContextDirtyResources,
		ContextError:             c.ContextError,
		CompactionRequestedAt:    c.CompactionRequestedAt,
		TitleSource:              c.TitleSource,
		TitleUpdatedAt:           c.TitleUpdatedAt,
	}
}

// ChildChatRow returns r in the row shape of upstream's
// GetChildChatsByParentIDs.
func (r GetDescendantChatsByRootIDsRow) ChildChatRow() database.GetChildChatsByParentIDsRow {
	return database.GetChildChatsByParentIDsRow{Chat: r.ChatsExpanded.Chat(), HasUnread: r.HasUnread}
}

// RBACObject authorizes a descendant as its chat, like upstream's
// GetChildChatsByParentIDsRow.
func (r GetDescendantChatsByRootIDsRow) RBACObject() rbac.Object {
	return r.ChatsExpanded.Chat().RBACObject()
}
