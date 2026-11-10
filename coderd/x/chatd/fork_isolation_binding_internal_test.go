package chatd

// This file is fork code: it tests how the fork-subagent-isolation experiment
// binds a worktree subagent's tools and prompt to its worktree.

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// recordingTool is a tool named name that records the input of each call.
type recordingTool struct {
	name   string
	inputs *[]string
}

func (r recordingTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: r.name} }

func (r recordingTool) Run(_ context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	*r.inputs = append(*r.inputs, call.Input)
	return fantasy.NewTextResponse("ok"), nil
}

func (recordingTool) ProviderOptions() fantasy.ProviderOptions     { return nil }
func (recordingTool) SetProviderOptions(_ fantasy.ProviderOptions) {}

// spawnChild spawns a child of parent with args and returns it.
func (f *isolationFixture) spawnChild(t *testing.T, parent database.Chat, args map[string]any) database.Chat {
	t.Helper()
	resp := f.spawn(t, parent, args)
	require.False(t, resp.IsError, resp.Content)
	var result struct {
		ChatID string `json:"chat_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
	childID, err := uuid.Parse(result.ChatID)
	require.NoError(t, err)
	child, err := f.db.GetChatByID(chatdTestContext(t), childID)
	require.NoError(t, err)
	return child
}

// systemTexts returns the text of chat's system messages, in order.
func (f *isolationFixture) systemTexts(t *testing.T, chat database.Chat) []string {
	t.Helper()
	messages, err := f.db.GetChatMessagesForPromptByChatID(chatdTestContext(t), chat.ID)
	require.NoError(t, err)
	var texts []string
	for i, msg := range messages {
		if msg.Role != database.ChatMessageRoleSystem {
			continue
		}
		require.Less(t, i, len(messages)-1, "the child's prompt must stay the last message")
		parts, err := chatprompt.ParseContent(msg)
		require.NoError(t, err)
		for _, part := range parts {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

func TestForkWorktreeBindingTools(t *testing.T) {
	t.Parallel()

	const dir = "/home/coder/.coder/worktrees/repo/child"
	f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
	root := f.rootChat(t, &fakeWorktreeConn{worktree: workspacesdk.ForkWorktree{Path: dir, Branch: "agent/child"}})
	worktreeChild := f.spawnChild(t, root, map[string]any{"isolation": "worktree"})

	bind := func(t *testing.T, chat database.Chat, name string) (fantasy.AgentTool, *[]string) {
		t.Helper()
		var inputs []string
		tools, err := f.server.appendForkTools(chatdTestContext(t), []fantasy.AgentTool{recordingTool{name: name, inputs: &inputs}}, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		require.Len(t, tools, 1)
		return tools[0], &inputs
	}

	for _, tc := range []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{name: "ExecuteNoWorkdir", tool: "execute", input: `{"command":"ls"}`, want: `{"command":"ls","workdir":"` + dir + `"}`},
		{name: "ExecuteNullWorkdir", tool: "execute", input: `{"command":"ls","workdir":null}`, want: `{"command":"ls","workdir":"` + dir + `"}`},
		{name: "ExecuteRelativeWorkdir", tool: "execute", input: `{"command":"ls","workdir":"sub/dir"}`, want: `{"command":"ls","workdir":"` + dir + `/sub/dir"}`},
		{name: "ExecuteAbsoluteWorkdir", tool: "execute", input: `{"command":"ls","workdir":"/tmp"}`, want: `{"command":"ls","workdir":"/tmp"}`},
		{name: "ReadFileRelative", tool: "read_file", input: `{"path":"main.go","offset":2}`, want: `{"offset":2,"path":"` + dir + `/main.go"}`},
		{name: "ReadFileAbsolute", tool: "read_file", input: `{"path":"/etc/hosts"}`, want: `{"path":"/etc/hosts"}`},
		{name: "WriteFileSpaces", tool: "write_file", input: `{"path":" a/b.txt ","content":"x"}`, want: `{"content":"x","path":"` + dir + `/a/b.txt"}`},
		{name: "AttachFileRelative", tool: "attach_file", input: `{"path":"out.png"}`, want: `{"path":"` + dir + `/out.png"}`},
		{
			name:  "EditFiles",
			tool:  "edit_files",
			input: `{"files":[{"path":"x.go","edits":[{"search":"a","replace":"b"}]},{"path":"/abs/y.go","edits":[]}]}`,
			want:  `{"files":[{"edits":[{"search":"a","replace":"b"}],"path":"` + dir + `/x.go"},{"edits":[],"path":"/abs/y.go"}]}`,
		},
		{name: "Unbound", tool: "process_list", input: `{"path":"rel"}`, want: `{"path":"rel"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool, inputs := bind(t, worktreeChild, tc.tool)
			_, err := tool.Run(chatdTestContext(t), fantasy.ToolCall{Name: tc.tool, Input: tc.input})
			require.NoError(t, err)
			require.Len(t, *inputs, 1)
			require.JSONEq(t, tc.want, (*inputs)[0])
		})
	}

	t.Run("WorkspaceContextDirectory", func(t *testing.T) {
		t.Parallel()
		agent := database.WorkspaceAgent{OperatingSystem: "linux", Directory: "/repo", ExpandedDirectory: "/repo"}
		require.Equal(t, database.WorkspaceAgent{OperatingSystem: "linux", Directory: dir, ExpandedDirectory: dir},
			f.server.forkWorktreeAgent(chatdTestContext(t), worktreeChild, agent))
		require.Equal(t, agent, f.server.forkWorktreeAgent(chatdTestContext(t), root, agent))
	})

	t.Run("SharedChildOfRoot", func(t *testing.T) {
		t.Parallel()
		sharedChild := f.spawnChild(t, root, map[string]any{})
		for _, chat := range []database.Chat{root, sharedChild} {
			tool, inputs := bind(t, chat, "execute")
			_, err := tool.Run(chatdTestContext(t), fantasy.ToolCall{Name: "execute", Input: `{"command":"ls"}`})
			require.NoError(t, err)
			require.Equal(t, []string{`{"command":"ls"}`}, *inputs)
		}
	})
}

func TestForkWorktreeBindingPrompt(t *testing.T) {
	t.Parallel()

	const dir = "/home/coder/.coder/worktrees/repo/child"
	f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation, codersdk.ExperimentForkNestedSubagents})
	conn := &fakeWorktreeConn{worktree: workspacesdk.ForkWorktree{Path: dir, Branch: "agent/child"}}
	root := f.rootChat(t, conn)
	child := f.spawnChild(t, root, map[string]any{"isolation": "worktree"})

	ownPrompt := "This chat works in its own git worktree at " + dir + ", on branch agent/child."
	require.Contains(t, f.systemTexts(t, child)[len(f.systemTexts(t, child))-1], ownPrompt)

	t.Run("SharedGrandchildInheritsWorktree", func(t *testing.T) {
		t.Parallel()
		grandchild := f.spawnChild(t, child, map[string]any{})
		texts := f.systemTexts(t, grandchild)
		require.Contains(t, texts[len(texts)-1], "This chat shares its parent chat's git worktree at "+dir+", on branch agent/child.")
		worktree, ok, err := f.server.chatWorktree(chatdTestContext(t), grandchild)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, subagentWorktree{path: dir, branch: "agent/child"}, worktree)
	})

	t.Run("WorktreeGrandchildBranchesFromWorktree", func(t *testing.T) {
		t.Parallel()
		grandchild := f.spawnChild(t, child, map[string]any{"isolation": "worktree"})
		creates, _ := conn.calls()
		var repoPath string
		for _, create := range creates {
			if create.Name == grandchild.ID.String() {
				repoPath = create.RepoPath
			}
		}
		require.Equal(t, dir, repoPath)
	})

	t.Run("SharedChildOfRootHasNoWorktreePrompt", func(t *testing.T) {
		t.Parallel()
		shared := f.spawnChild(t, root, map[string]any{})
		for _, text := range f.systemTexts(t, shared) {
			require.NotContains(t, text, "worktree")
		}
	})
}
