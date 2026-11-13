package chatd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestChatNotesTool(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	// The authorizing store proves the notes queries pass dbauthz as chatd.
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkTokenBudget}))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)

	chat, err := server.CreateChat(ctx, CreateOptions{OrganizationID: org.ID, OwnerID: user.ID, Title: "notes", ModelConfigID: model.ID, InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")}})
	require.NoError(t, err)
	other, err := server.CreateChat(ctx, CreateOptions{OrganizationID: org.ID, OwnerID: user.ID, Title: "other", ModelConfigID: model.ID, InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")}})
	require.NoError(t, err)
	require.NoError(t, WaitUntilIdleForTest(ctx, server))

	tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
	require.NoError(t, err)
	require.Equal(t, []string{"notes", "chat_history", "new_context"}, toolNames(tools))
	// The calls of a step run in call order, so a read sees the writes
	// before it and the note cap holds.
	notesTool := findToolByName(tools, chatNotesToolName)
	serial, ok := notesTool.(interface{ SerialToolCalls() bool })
	require.True(t, ok && serial.SerialToolCalls(), "notes calls must run one at a time")
	// Plan mode, explore and read-only custom agents do not get notes.
	require.Empty(t, filterToolsForTurn([]fantasy.AgentTool{notesTool}, database.NullChatPlanMode{ChatPlanMode: database.ChatPlanModePlan, Valid: true}, uuid.NullUUID{}, nil))
	require.False(t, exploreToolAllowed(notesTool))
	require.False(t, customAgentReadOnlyToolAllowed(notesTool))
	otherTools, err := server.appendForkTools(ctx, nil, other, other.LastModelConfigID)
	require.NoError(t, err)

	run := func(args chatNotesArgs, wantError bool) string {
		t.Helper()
		return runChatTodoTool(ctx, t, tools, chatNotesToolName, args, wantError)
	}
	read := func(path string) string {
		t.Helper()
		var note chatNoteResult
		require.NoError(t, json.Unmarshal([]byte(run(chatNotesArgs{Action: "read", Path: path}, false)), &note))
		return note.Content
	}

	require.JSONEq(t, `{"notes": []}`, run(chatNotesArgs{Action: "list"}, false))

	// write, append and read.
	run(chatNotesArgs{Action: "write", Path: "checkpoint.md", Content: "goal"}, false)
	run(chatNotesArgs{Action: "append", Path: "checkpoint.md", Content: "next"}, false)
	require.Equal(t, "goal\nnext", read("checkpoint.md"))
	// write replaces the note.
	run(chatNotesArgs{Action: "write", Path: "checkpoint.md", Content: "replaced"}, false)
	require.Equal(t, "replaced", read("checkpoint.md"))
	// append creates a missing note, and appending to an empty note adds no
	// leading newline.
	run(chatNotesArgs{Action: "append", Path: "findings/a.md", Content: "first"}, false)
	require.Equal(t, "first", read("findings/a.md"))
	run(chatNotesArgs{Action: "write", Path: "empty.md"}, false)
	run(chatNotesArgs{Action: "append", Path: "empty.md", Content: "line"}, false)
	require.Equal(t, "line", read("empty.md"))

	var list chatNotesListResult
	require.NoError(t, json.Unmarshal([]byte(run(chatNotesArgs{Action: "list"}, false)), &list))
	paths := make([]string, 0, len(list.Notes))
	for _, note := range list.Notes {
		paths = append(paths, note.Path)
	}
	require.Equal(t, []string{"checkpoint.md", "empty.md", "findings/a.md"}, paths)
	require.EqualValues(t, len("replaced"), list.Notes[0].Bytes)

	// delete removes a note, and deleting a missing note is not an error.
	require.JSONEq(t, `{"path": "empty.md", "deleted": true}`, run(chatNotesArgs{Action: "delete", Path: "empty.md"}, false))
	require.JSONEq(t, `{"path": "empty.md", "deleted": false}`, run(chatNotesArgs{Action: "delete", Path: "empty.md"}, false))

	require.Contains(t, run(chatNotesArgs{Action: "read", Path: "missing.md"}, true), "does not exist")
	require.Contains(t, run(chatNotesArgs{Action: "rename", Path: "checkpoint.md"}, true), "unknown action")
	for _, path := range []string{"", "Checkpoint.md", "../x", "/abs", ".hidden", "a b", strings.Repeat("a", 129)} {
		require.Contains(t, run(chatNotesArgs{Action: "write", Path: path, Content: "x"}, true), "invalid note path", path)
	}

	// The size cap holds for write and for append.
	require.Contains(t, run(chatNotesArgs{Action: "write", Path: "big.md", Content: strings.Repeat("b", maxChatNoteBytes+1)}, true), "bytes at most")
	run(chatNotesArgs{Action: "write", Path: "big.md", Content: strings.Repeat("b", maxChatNoteBytes)}, false)
	require.Contains(t, run(chatNotesArgs{Action: "append", Path: "big.md", Content: "b"}, true), "would be over")
	require.Len(t, read("big.md"), maxChatNoteBytes)

	// The note cap: three notes exist, so 17 more fit.
	for i := range maxChatNotes - 3 {
		run(chatNotesArgs{Action: "write", Path: fmt.Sprintf("n%02d.md", i), Content: "x"}, false)
	}
	require.Contains(t, run(chatNotesArgs{Action: "write", Path: "one-too-many.md", Content: "x"}, true), "the most it keeps")
	require.Contains(t, run(chatNotesArgs{Action: "append", Path: "one-too-many.md", Content: "x"}, true), "the most it keeps")
	// Notes that exist stay writable at the cap, and a delete frees a slot.
	run(chatNotesArgs{Action: "append", Path: "checkpoint.md", Content: "more"}, false)
	require.Equal(t, "replaced\nmore", read("checkpoint.md"))
	run(chatNotesArgs{Action: "delete", Path: "n00.md"}, false)
	run(chatNotesArgs{Action: "write", Path: "one-too-many.md", Content: "x"}, false)

	// Another chat does not see these notes.
	require.JSONEq(t, `{"notes": []}`, runChatTodoTool(ctx, t, otherTools, chatNotesToolName, chatNotesArgs{Action: "list"}, false))
	require.Contains(t, runChatTodoTool(ctx, t, otherTools, chatNotesToolName, chatNotesArgs{Action: "read", Path: "checkpoint.md"}, true), "does not exist")

	// With the experiment off, even with every upstream experiment on, the
	// chat gets no notes tool.
	upstream := slices.DeleteFunc(slices.Clone(codersdk.ExperimentsKnown), func(e codersdk.Experiment) bool {
		return slices.Contains(codersdk.ForkExperiments, e)
	})
	offServer := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{}, withInternalTestServerExperiments(upstream))
	offTools, err := offServer.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
	require.NoError(t, err)
	require.Empty(t, toolNames(offTools))
}
