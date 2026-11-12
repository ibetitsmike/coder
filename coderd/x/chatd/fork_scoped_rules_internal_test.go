package chatd

// This file is fork code: it tests attaching glob-scoped rules by touched
// files.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/codersdk"
)

func globRuleResource(t *testing.T, source, content string, globs ...string) database.ChatContextResource {
	t.Helper()
	return database.ChatContextResource{
		Source:   source,
		BodyKind: database.WorkspaceAgentContextBodyKindInstructionFile,
		Body:     mustMarshalContextBody(t, &agentproto.InstructionFileBody{Content: []byte(content), Globs: globs}),
		Status:   database.WorkspaceAgentContextResourceStatusOk,
	}
}

func toolCallMessage(t *testing.T, calls map[string]string) database.ChatMessage {
	t.Helper()
	var parts []codersdk.ChatMessagePart
	for name, args := range calls {
		parts = append(parts, codersdk.ChatMessagePart{
			Type:       codersdk.ChatMessagePartTypeToolCall,
			ToolCallID: name,
			ToolName:   name,
			Args:       json.RawMessage(args),
		})
	}
	content, err := chatprompt.MarshalParts(parts)
	require.NoError(t, err)
	return database.ChatMessage{
		Role:           database.ChatMessageRoleAssistant,
		Content:        content,
		ContentVersion: chatprompt.ContentVersionV1,
	}
}

func TestTouchedFilePaths(t *testing.T) {
	t.Parallel()
	rows := []database.ChatMessage{
		toolCallMessage(t, map[string]string{"read_file": `{"path":"/p/a.go"}`}),
		toolCallMessage(t, map[string]string{"write_file": `{"path":"/p/b.go","content":"x"}`}),
		toolCallMessage(t, map[string]string{"edit_files": `{"files":[{"path":"/p/c.go"},{"path":"/p/a.go"}]}`}),
		toolCallMessage(t, map[string]string{"execute": `{"command":"cat /p/d.go","path":"/p/d.go"}`}),
	}
	require.Equal(t, []string{"/p/a.go", "/p/b.go", "/p/c.go"}, touchedFilePaths(rows))
}

func TestContextResourcesToPrompt_ScopedRules(t *testing.T) {
	t.Parallel()
	const (
		dir      = "/home/coder"
		ruleBody = "TS rule body"
	)
	agents := instructionResource(t, "/home/coder/proj/AGENTS.md", "be helpful", database.WorkspaceAgentContextResourceStatusOk)
	projectRule := "/home/coder/proj/.cursor/rules/ts.mdc"

	t.Run("NotInAlwaysOnBlock", func(t *testing.T) {
		t.Parallel()
		instruction, _, malformed := contextResourcesToPrompt([]database.ChatContextResource{
			globRuleResource(t, projectRule, ruleBody, "*.ts"),
		}, "linux", dir)
		require.Zero(t, malformed)
		require.Empty(t, instruction, "a glob rule alone must not produce an instruction block")
	})

	tests := []struct {
		name    string
		source  string
		globs   []string
		dir     string
		touched []string
		attach  bool
	}{
		{name: "UnrelatedFile", source: projectRule, globs: []string{"*.ts"}, dir: dir, touched: []string{"/home/coder/proj/README.md"}},
		{name: "MatchingFileAtAnyDepth", source: projectRule, globs: []string{"*.ts"}, dir: dir, touched: []string{"/home/coder/proj/README.md", "/home/coder/proj/src/app.ts"}, attach: true},
		{name: "RelativeToProjectRoot", source: projectRule, globs: []string{"src/*.ts"}, dir: dir, touched: []string{"/home/coder/proj/src/app.ts"}, attach: true},
		{name: "AnchoredGlobOutsidePrefix", source: projectRule, globs: []string{"src/*.ts"}, dir: dir, touched: []string{"/home/coder/src/app.ts", "/home/coder/proj/lib/src/app.ts"}},
		{name: "OutsideProjectRoot", source: projectRule, globs: []string{"**/*.ts"}, dir: dir, touched: []string{"/home/coder/other/app.ts"}},
		{name: "RelativeTouchedPath", source: projectRule, globs: []string{"src/*.ts"}, dir: "/home/coder/proj", touched: []string{"src/app.ts"}, attach: true},
		{name: "UserRuleRootIsHome", source: "/home/coder/.coder/rules/go.mdc", globs: []string{"proj/**/*.go"}, dir: "/home/coder/proj", touched: []string{"/home/coder/proj/cmd/main.go"}, attach: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resources := []database.ChatContextResource{agents, globRuleResource(t, tt.source, ruleBody, tt.globs...)}
			instruction, _, malformed := contextResourcesToPrompt(resources, "linux", tt.dir, tt.touched...)
			require.Zero(t, malformed)
			require.Contains(t, instruction, "be helpful")
			if tt.attach {
				require.Contains(t, instruction, "Source: "+tt.source)
				require.Contains(t, instruction, ruleBody)
			} else {
				require.NotContains(t, instruction, ruleBody)
			}
		})
	}
}
