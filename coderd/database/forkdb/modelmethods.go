package forkdb

import (
	"github.com/coder/coder/v2/coderd/rbac"
)

func (p Plugin) RBACObject() rbac.Object {
	return rbac.ResourcePlugin.WithID(p.ID).InOrg(p.OrganizationID)
}
