package chatd

// This file is fork code: it tests the custom agent tool policy behind the
// fork-agent-definitions experiment.

import (
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/codersdk"
)

type testWorkspaceMCPAgentTool struct {
	*testAgentTool
	server string
}

func (t *testWorkspaceMCPAgentTool) ServerName() string {
	return t.server
}

// TestCustomAgentToolPolicySkipsLookup checks that a chat without the
// experiment, or a root chat, keeps its tools without reading the database:
// the server has no store, so any read fails the call.
func TestCustomAgentToolPolicySkipsLookup(t *testing.T) {
	t.Parallel()

	parent := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	tests := []struct {
		name        string
		experiments codersdk.Experiments
		chat        database.Chat
	}{
		{name: "ExperimentOff", chat: database.Chat{ID: uuid.New(), ParentChatID: parent}},
		{
			name:        "RootChat",
			experiments: codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
			chat:        database.Chat{ID: uuid.New()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := &Server{experiments: tt.experiments}
			tools := []fantasy.AgentTool{newTestAgentTool("write_file")}
			candidates := []deferredMCPTool{{tool: newTestMCPAgentTool("docs__search", uuid.New()), server: "docs"}}
			gotTools, gotCandidates, _, err := server.applyCustomAgentToolPolicy(chatdTestContext(t), tt.chat, tools, candidates, nil)
			require.NoError(t, err)
			require.Equal(t, tools, gotTools)
			require.Equal(t, candidates, gotCandidates)
		})
	}
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
	// Workspace config can hold a padded server name next to the plain one.
	padded := &testWorkspaceMCPAgentTool{
		testAgentTool: &testAgentTool{info: fantasy.ToolInfo{Name: "_Workspace__ping"}},
		server:        " Workspace ",
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
		// An MCP tool whose server is unknown has no server name.
		newTestMCPAgentTool("ghost__lookup", uuid.New()),
		workspace,
		padded,
	}
	candidates := []deferredMCPTool{
		{tool: docs, server: "docs"}, {tool: other, server: "other"},
		{tool: workspace, server: "Workspace"}, {tool: padded, server: " Workspace "},
	}
	providerTools := []chatloop.ProviderTool{
		{Definition: fantasy.ProviderDefinedTool{Name: "web_search"}},
		{Definition: fantasy.ProviderDefinedTool{Name: "code_execution"}},
	}
	builtins := []string{"read_file", "write_file", "edit_files", "execute", "process_output", "spawn_agent"}
	allMCP := []string{"docs__search", "other__lookup", "ghost__lookup", "Workspace__echo", "_Workspace__ping"}
	allDeferred := []string{"docs__search", "other__lookup", "Workspace__echo", "_Workspace__ping"}
	allProvider := []string{"web_search", "code_execution"}

	tests := []struct {
		name         string
		policy       customAgentToolPolicy
		want         []string
		wantDeferred []string
		wantProvider []string
	}{
		{
			name:         "Inherit",
			policy:       customAgentToolPolicy{MCPMode: "inherit"},
			want:         append(append([]string{}, builtins...), allMCP...),
			wantDeferred: allDeferred,
			wantProvider: allProvider,
		},
		{
			name:         "ReadOnly",
			policy:       customAgentToolPolicy{ReadOnly: true, MCPMode: "inherit"},
			want:         []string{"read_file", "execute", "process_output", "docs__search", "other__lookup", "ghost__lookup"},
			wantDeferred: []string{"docs__search", "other__lookup"},
			wantProvider: []string{"web_search"},
		},
		{
			name:         "AllowlistAliases",
			policy:       customAgentToolPolicy{Tools: []string{"Grep", "WebSearch"}, MCPMode: "inherit"},
			want:         append([]string{"read_file", "execute", "process_output"}, allMCP...),
			wantDeferred: allDeferred,
			wantProvider: []string{"web_search"},
		},
		{
			name:         "EmptyAllowlist",
			policy:       customAgentToolPolicy{Tools: []string{}, MCPMode: "inherit"},
			want:         allMCP,
			wantDeferred: allDeferred,
			wantProvider: []string{},
		},
		{
			name:         "Disallowed",
			policy:       customAgentToolPolicy{DisallowedTools: []string{"execute", "mcp__other__lookup", "ghost__lookup", "web_search"}, MCPMode: "inherit"},
			want:         []string{"read_file", "write_file", "edit_files", "process_output", "spawn_agent", "docs__search", "Workspace__echo", "_Workspace__ping"},
			wantDeferred: []string{"docs__search", "Workspace__echo", "_Workspace__ping"},
			wantProvider: []string{"code_execution"},
		},
		{
			name:         "MCPNone",
			policy:       customAgentToolPolicy{MCPMode: "none"},
			want:         builtins,
			wantDeferred: []string{},
			wantProvider: allProvider,
		},
		{
			name:         "MCPNamed",
			policy:       customAgentToolPolicy{MCPMode: "named", MCPServers: []string{"docs", "Workspace"}},
			want:         append(append([]string{}, builtins...), "docs__search", "Workspace__echo"),
			wantDeferred: []string{"docs__search", "Workspace__echo"},
			wantProvider: allProvider,
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

			gotTools, gotCandidates, gotProvider := policy.filter(tools, candidates, providerTools, slugs)
			gotDeferred := make([]string, 0, len(gotCandidates))
			for _, candidate := range gotCandidates {
				gotDeferred = append(gotDeferred, candidate.tool.Info().Name)
			}
			gotProviderNames := make([]string, 0, len(gotProvider))
			for _, tool := range gotProvider {
				gotProviderNames = append(gotProviderNames, tool.Definition.GetName())
			}
			require.Equal(t, tt.want, toolNames(gotTools))
			require.Equal(t, tt.wantDeferred, gotDeferred)
			require.Equal(t, tt.wantProvider, gotProviderNames)
		})
	}
}
