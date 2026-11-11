package forkdb

import (
	"slices"

	"github.com/coder/coder/v2/coderd/database"
)

// ProvisionerTypeSandbox is the provisioner_type value that the sandbox
// provisioner node adds. Upstream's generated database.ProvisionerType.Valid
// does not know it, so validate provisioner types with ValidProvisionerType.
const ProvisionerTypeSandbox database.ProvisionerType = "sandbox"

// ProvisionerTypeValues returns the provisioner_type values that fork nodes
// add.
func ProvisionerTypeValues() []database.ProvisionerType {
	return []database.ProvisionerType{
		ProvisionerTypeSandbox,
	}
}

// ValidProvisionerType reports whether t is a value of the provisioner_type
// enum, counting the values that fork nodes add.
func ValidProvisionerType(t database.ProvisionerType) bool {
	return t.Valid() || slices.Contains(ProvisionerTypeValues(), t)
}
