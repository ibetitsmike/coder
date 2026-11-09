package workspacesdk_test

// This file is fork code: it tests the fork-only agent definition parser.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestParseAgentDefinition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		fallback string
		want     workspacesdk.AgentDefinition
	}{
		{
			name: "PstackCommentSicko",
			content: "---\nname: comment-sicko\ndescription: Hunts slop comments in a diff.\n---\n" +
				"<!-- maintainer note -->\nYou review comments.\n",
			fallback: "other",
			want: workspacesdk.AgentDefinition{
				Name:        "comment-sicko",
				Description: "Hunts slop comments in a diff.",
				Prompt:      "You review comments.",
				MCPMode:     workspacesdk.AgentDefinitionMCPInherit,
				BaseType:    workspacesdk.AgentDefinitionBaseGeneral,
			},
		},
		{
			name:     "PstackPotetoAgentCursor",
			content:  "\xef\xbb\xbf---\nname: poteto-agent\ndescription: Runs the poteto loop.\nis_background: true\nreadonly: true\nmodel: fast\n---\nDo the loop.",
			fallback: "poteto-agent",
			want: workspacesdk.AgentDefinition{
				Name:        "poteto-agent",
				Description: "Runs the poteto loop.",
				Prompt:      "Do the loop.",
				Model:       "fast",
				ReadOnly:    true,
				MCPMode:     workspacesdk.AgentDefinitionMCPInherit,
				BaseType:    workspacesdk.AgentDefinitionBaseGeneral,
			},
		},
		{
			name: "ClaudeCode",
			content: "---\nname: code-reviewer\ndescription: Reviews code.\ntools: Read, Grep, , Glob\n" +
				"disallowedTools: [Write]\nmodel: inherit\ncolor: blue\nmcpServers: [github]\n---\nReview it.",
			want: workspacesdk.AgentDefinition{
				Name:            "code-reviewer",
				Description:     "Reviews code.",
				Prompt:          "Review it.",
				Tools:           []string{"Read", "Grep", "Glob"},
				DisallowedTools: []string{"Write"},
				MCPMode:         workspacesdk.AgentDefinitionMCPNamed,
				MCPServers:      []string{"github"},
				BaseType:        workspacesdk.AgentDefinitionBaseGeneral,
			},
		},
		{
			name: "CoderKeys",
			content: "---\ndescription: Explores.\ntools:\n  - read_file\n  - execute\ndisallowed_tools: execute\n" +
				"reasoning_effort: high\nmcp: none\nbase_type: explore\n---\nExplore.",
			fallback: "explorer",
			want: workspacesdk.AgentDefinition{
				Name:            "explorer",
				Description:     "Explores.",
				Prompt:          "Explore.",
				ReasoningEffort: "high",
				Tools:           []string{"read_file", "execute"},
				DisallowedTools: []string{"execute"},
				MCPMode:         workspacesdk.AgentDefinitionMCPNone,
				BaseType:        workspacesdk.AgentDefinitionBaseExplore,
			},
		},
		{
			name:     "WarningsSorted",
			content:  "---\nname:\nzeta: 1\nalpha: 2\nmcp: [linear]\nmcpServers: [github]\n---\nBody.",
			fallback: "fallback-name",
			want: workspacesdk.AgentDefinition{
				Name:       "fallback-name",
				Prompt:     "Body.",
				MCPMode:    workspacesdk.AgentDefinitionMCPNamed,
				MCPServers: []string{"linear"},
				BaseType:   workspacesdk.AgentDefinitionBaseGeneral,
				Warnings: []string{
					"mcpServers ignored because mcp is set",
					"missing description",
					`unknown front-matter field "alpha" ignored`,
					`unknown front-matter field "zeta" ignored`,
				},
			},
		},
		{
			name:    "InlineMCPServersIgnored",
			content: "---\nname: a\ndescription: d\nmcpServers:\n  - github\n  - slack:\n      command: slack-mcp\n---\nBody.",
			want: workspacesdk.AgentDefinition{
				Name:        "a",
				Description: "d",
				Prompt:      "Body.",
				MCPMode:     workspacesdk.AgentDefinitionMCPNamed,
				MCPServers:  []string{"github"},
				BaseType:    workspacesdk.AgentDefinitionBaseGeneral,
				Warnings:    []string{"inline MCP server definitions in mcpServers are not supported and were ignored"},
			},
		},
		{
			name:    "InlineMCPServersMap",
			content: "---\nname: a\ndescription: d\nmcpServers:\n  slack:\n    command: slack-mcp\n---\nBody.",
			want: workspacesdk.AgentDefinition{
				Name:        "a",
				Description: "d",
				Prompt:      "Body.",
				MCPMode:     workspacesdk.AgentDefinitionMCPNamed,
				MCPServers:  []string{},
				BaseType:    workspacesdk.AgentDefinitionBaseGeneral,
				Warnings:    []string{"inline MCP server definitions in mcpServers are not supported and were ignored"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := workspacesdk.ParseAgentDefinition(tt.content, tt.fallback)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseAgentDefinitionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		fallback string
		errMsg   string
	}{
		{name: "NonKebabName", content: "---\nname: Code_Reviewer\n---\nBody.", errMsg: `agent name "Code_Reviewer" is not kebab-case`},
		{name: "NonKebabFallback", content: "---\ndescription: d\n---\nBody.", fallback: "My Agent", errMsg: `agent name "My Agent" is not kebab-case`},
		{name: "EmptyBody", content: "---\nname: a\n---\n<!-- only a comment -->\n", errMsg: "agent definition has no prompt body"},
		{name: "BadBaseType", content: "---\nname: a\nbase_type: plan\n---\nBody.", errMsg: `"base_type"`},
		{name: "ReadonlyNotBool", content: "---\nname: a\nreadonly: \"yes\"\n---\nBody.", errMsg: `"readonly"`},
		{name: "ToolsWrongType", content: "---\nname: a\ntools: {read: true}\n---\nBody.", errMsg: `"tools"`},
		{name: "DisallowedToolsNonString", content: "---\nname: a\ndisallowedTools: [1]\n---\nBody.", errMsg: `"disallowedTools"`},
		{name: "DescriptionWrongType", content: "---\nname: a\ndescription: [x]\n---\nBody.", errMsg: `"description"`},
		{name: "BadMCPMode", content: "---\nname: a\nmcp: all\n---\nBody.", errMsg: `"mcp"`},
		{name: "UnclosedFrontmatter", content: "---\nname: a\nBody.", errMsg: "missing closing frontmatter delimiter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := workspacesdk.ParseAgentDefinition(tt.content, tt.fallback)
			require.ErrorContains(t, err, tt.errMsg)
			require.NotErrorIs(t, err, workspacesdk.ErrNotAgentDefinition)
		})
	}

	t.Run("NoFrontmatter", func(t *testing.T) {
		t.Parallel()
		for _, content := range []string{"", "# Agents\n\nThis directory holds agents.\n", "text\n---\nname: a\n---\nBody."} {
			_, err := workspacesdk.ParseAgentDefinition(content, "readme")
			require.ErrorIs(t, err, workspacesdk.ErrNotAgentDefinition, "content %q", content)
		}
	})
}
