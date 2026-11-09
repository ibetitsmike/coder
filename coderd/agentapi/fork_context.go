package agentapi

// This file is fork code: it persists the fork-only subagent context resource
// body that carries custom agent definitions.

import (
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
)

func marshalSubagentBody(b *agentproto.ContextResource_Subagent) (database.WorkspaceAgentContextBodyKind, []byte, error) {
	payload := b.Subagent
	if payload == nil {
		payload = &agentproto.SubagentBody{}
	}
	body, err := marshalBody(payload)
	return database.WorkspaceAgentContextBodyKindSubagent, body, err
}
