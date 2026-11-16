package coderd_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/audit"
	"github.com/coder/coder/v2/coderd/database"
)

// forkAuditEntry is an audit entry of a fork resource change, with its
// additional fields decoded.
type forkAuditEntry struct {
	Log          database.AuditLog
	Operation    string `json:"operation"`
	ResourceID   string `json:"resource_id"`
	ResourceName string `json:"resource_name"`
	Changes      map[string]struct {
		Old    json.RawMessage `json:"old"`
		New    json.RawMessage `json:"new"`
		Secret bool            `json:"secret"`
	} `json:"changes"`
}

// forkAuditEntries returns the audit entries of one fork resource type. Each
// is a write of the organization orgID.
func forkAuditEntries(t *testing.T, auditor *audit.MockAuditor, orgID uuid.UUID, resource string) []forkAuditEntry {
	t.Helper()
	var entries []forkAuditEntry
	for _, log := range auditor.AuditLogs() {
		var fields struct {
			Resource string `json:"fork_resource"`
		}
		if json.Unmarshal(log.AdditionalFields, &fields) != nil || fields.Resource != resource {
			continue
		}
		require.Equal(t, database.ResourceTypeOrganization, log.ResourceType)
		require.Equal(t, database.AuditActionWrite, log.Action)
		require.Equal(t, orgID, log.ResourceID)
		require.Equal(t, orgID, log.OrganizationID)
		entry := forkAuditEntry{Log: log}
		require.NoError(t, json.Unmarshal(log.AdditionalFields, &entry))
		entries = append(entries, entry)
	}
	return entries
}
