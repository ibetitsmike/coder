package chatd

// This file is fork code: it tests the custom agent tool policy behind the
// fork-agent-definitions experiment.

import (
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type testWorkspaceMCPAgentTool struct {
	*testAgentTool
	server string
}

func (t *testWorkspaceMCPAgentTool) ServerName() string {
	return t.server
}

func TestCustomAgentToolPolicyFilter(t *testing.T) {
	t.Parallel()

	docsID, otherID := uuid.New(), uuid.New()
	slugs := map[uuid.UUID]string{docsID: "docs", otherID: "other"}
	docs := newTestMCPAgentTool("docs__search", docsID)
	other := newTestMCPAgentTool("other__lookup", otherID)
	workspace := &testWorkspaceMCPAgentTool{
		testAgentTool: &testAgentTool{info: fantasy.ToolInfo{Name: "Workspace__echo"}},
		server:        "Workspace",
	}
	tools := []fantasy.AgentTool{
		newTestAgentTool("read_file"),
		newTestAgentTool("write_file"),
		newTestAgentTool("edit_files"),
		newTestAgentTool("execute"),
		newTestAgentTool("process_output"),
		newTestAgentTool("spawn_agent"),
		docs,
		other,
		// An MCP tool whose server config is unknown has no server name.
		newTestMCPAgentTool("ghost__lookup", uuid.New()),
		workspace,
	}
	candidates := []deferredMCPTool{{tool: docs, server: "docs"}, {tool: other, server: "other"}, {tool: workspace, server: "Workspace"}}
	builtins := []string{"read_file", "write_file", "edit_files", "execute", "process_output", "spawn_agent"}
	allMCP := []string{"docs__search", "other__lookup", "ghost__lookup", "Workspace__echo"}
	allDeferred := []string{"docs__search", "other__lookup", "Workspace__echo"}

	tests := []struct {
		name         string
		policy       customAgentToolPolicy
		want         []string
		wantDeferred []string
	}{
		{
			name:         "Inherit",
			policy:       customAgentToolPolicy{MCPMode: "inherit"},
			want:         append(append([]string{}, builtins...), allMCP...),
			wantDeferred: allDeferred,
		},
		{
			name:         "ReadOnly",
			policy:       customAgentToolPolicy{ReadOnly: true, MCPMode: "inherit"},
			want:         []string{"read_file", "execute", "process_output", "docs__search", "other__lookup", "ghost__lookup"},
			wantDeferred: []string{"docs__search", "other__lookup"},
		},
		{
			name:         "AllowlistAliases",
			policy:       customAgentToolPolicy{Tools: []string{"Read", "Bash"}, MCPMode: "inherit"},
			want:         append([]string{"read_file", "execute", "process_output"}, allMCP...),
			wantDeferred: allDeferred,
		},
		{
			name:         "EmptyAllowlist",
			policy:       customAgentToolPolicy{Tools: []string{}, MCPMode: "inherit"},
			want:         allMCP,
			wantDeferred: allDeferred,
		},
		{
			name:         "Disallowed",
			policy:       customAgentToolPolicy{DisallowedTools: []string{"execute", "other__lookup"}, MCPMode: "inherit"},
			want:         []string{"read_file", "write_file", "edit_files", "process_output", "spawn_agent", "docs__search", "ghost__lookup", "Workspace__echo"},
			wantDeferred: []string{"docs__search", "Workspace__echo"},
		},
		{
			name:         "MCPNone",
			policy:       customAgentToolPolicy{MCPMode: "none"},
			want:         builtins,
			wantDeferred: []string{},
		},
		{
			name:         "MCPNamed",
			policy:       customAgentToolPolicy{MCPMode: "named", MCPServers: []string{"DOCS", "workspace"}},
			want:         append(append([]string{}, builtins...), "docs__search", "Workspace__echo"),
			wantDeferred: []string{"docs__search", "Workspace__echo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Round-trip through the stored form, where a nil and an empty
			// allowlist must stay distinct.
			raw, err := json.Marshal(tt.policy)
			require.NoError(t, err)
			var policy customAgentToolPolicy
			require.NoError(t, json.Unmarshal(raw, &policy))

			gotTools, gotCandidates := policy.filter(tools, candidates, slugs)
			got := make([]string, 0, len(gotTools))
			for _, tool := range gotTools {
				got = append(got, tool.Info().Name)
			}
			gotDeferred := make([]string, 0, len(gotCandidates))
			for _, candidate := range gotCandidates {
				gotDeferred = append(gotDeferred, candidate.tool.Info().Name)
			}
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.wantDeferred, gotDeferred)
		})
	}
}
