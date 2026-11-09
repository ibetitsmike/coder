package forkdb

import (
	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/rbac"
)

func (p Plugin) RBACObject() rbac.Object {
	return rbac.ResourcePlugin.WithID(p.ID).InOrg(p.OrganizationID)
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

func organizationAgentDefinitionObject(id, organizationID uuid.UUID, groupACL, userACL database.ChatACL) rbac.Object {
	return rbac.ResourceOrganizationSkill.
		WithID(id).
		InOrg(organizationID).
		WithGroupACL(groupACL.RBACACL()).
		WithACLUserList(userACL.RBACACL())
}
