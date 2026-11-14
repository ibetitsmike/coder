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
