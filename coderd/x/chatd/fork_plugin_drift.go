package chatd

// This file is fork code: it derives the drift hash chats pin against for
// the fork-plugin-packs experiment (PRD 6.7). The agent excludes pack rows from
// its pushed aggregate unconditionally (agent/agentcontext/resolve.go
// driftResources) because it has no experiment knowledge; with the
// experiment on, pack rows ARE pinned prompt content, so chatd re-derives
// the hash from the persisted rows and includes them. With both fork
// experiments off and no subagent rows, chatd reproduces the agent's
// exclusion, making the derived hash byte-identical to the pushed one (same
// ComputeAggregateHash scheme, same filters).
//
// Deriving at push time and storing the result on the snapshot row keeps
// every chat pinning path consistent: the push path hydrates and dirties
// against the derived hash, while the create-time and refresh paths re-pin
// from the snapshot row and therefore see the same value.

import (
	"github.com/coder/coder/v2/agent/agentcontext"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// DriftHash implements agentapi.ContextDriftHasher: it rebuilds the
// agentcontext resources the rows encode and hashes them with the agent's
// scheme, minus MCP and ACP rows (live-synced on every push, never drift)
// and, while fork-plugin-packs is off, minus pack-contributed rows (invisible
// to chats). Every subagent row is also skipped while fork-agent-definitions
// is off: nothing consumes them then (custom agent types and the agent type
// catalog are disabled, and the context listing hides subagents), so adding,
// editing, or reclassifying an agent definition would otherwise dirty every
// chat without changing anything it can see. Unknown
// body kinds or statuses cannot occur today (the push validator rejects
// them); they are skipped rather than mis-hashed.
func (p *Server) DriftHash(rows []database.UpsertWorkspaceAgentContextResourceParams, _ []byte) []byte {
	pluginsOn := p.experiments.Enabled(codersdk.ExperimentForkPluginPacks)
	definitionsOn := p.experiments.Enabled(codersdk.ExperimentForkAgentDefinitions)
	resources := make([]agentcontext.Resource, 0, len(rows))
	for _, row := range rows {
		kind, ok := agentResourceKind(row.BodyKind)
		if !ok {
			continue
		}
		switch kind {
		case agentcontext.KindMCPConfig, agentcontext.KindMCPServer, agentcontext.KindACPHarness:
			continue
		}
		packRow := isPackContribution(row.SourcePath)
		if !pluginsOn && packRow {
			continue
		}
		if kind == agentcontext.KindSubagent && !definitionsOn {
			continue
		}
		status, ok := agentResourceStatus(row.Status)
		if !ok {
			continue
		}
		var contentHash [32]byte
		copy(contentHash[:], row.ContentHash)
		resources = append(resources, agentcontext.Resource{
			// resourceID builds the same kind:source ID the agent hashes.
			ID:          kind.String() + ":" + row.Source,
			Kind:        kind,
			Source:      row.Source,
			ContentHash: contentHash,
			Status:      status,
		})
	}
	hash := agentcontext.ComputeAggregateHash(resources)
	return hash[:]
}

// agentResourceKind maps a stored body kind back to the agentcontext kind
// whose String() produced it. plugin, hook, and command are reserved kinds
// with no wire body yet, so nothing stores them today.
func agentResourceKind(kind database.WorkspaceAgentContextBodyKind) (agentcontext.ResourceKind, bool) {
	switch kind {
	case database.WorkspaceAgentContextBodyKindInstructionFile:
		return agentcontext.KindInstructionFile, true
	case database.WorkspaceAgentContextBodyKindSkill:
		return agentcontext.KindSkill, true
	case database.WorkspaceAgentContextBodyKindMcpConfig:
		return agentcontext.KindMCPConfig, true
	case database.WorkspaceAgentContextBodyKindMcpServer:
		return agentcontext.KindMCPServer, true
	case database.WorkspaceAgentContextBodyKindSubagent:
		return agentcontext.KindSubagent, true
	case database.WorkspaceAgentContextBodyKindAcpHarness:
		return agentcontext.KindACPHarness, true
	default:
		return 0, false
	}
}

// agentResourceStatus maps a stored status back to the agentcontext status
// whose String() produced it; the two enums share their string values.
func agentResourceStatus(status database.WorkspaceAgentContextResourceStatus) (agentcontext.ResourceStatus, bool) {
	switch status {
	case database.WorkspaceAgentContextResourceStatusOk:
		return agentcontext.StatusOK, true
	case database.WorkspaceAgentContextResourceStatusOversize:
		return agentcontext.StatusOversize, true
	case database.WorkspaceAgentContextResourceStatusUnreadable:
		return agentcontext.StatusUnreadable, true
	case database.WorkspaceAgentContextResourceStatusInvalid:
		return agentcontext.StatusInvalid, true
	case database.WorkspaceAgentContextResourceStatusExcluded:
		return agentcontext.StatusExcluded, true
	default:
		return 0, false
	}
}
