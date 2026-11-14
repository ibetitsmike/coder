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
	"golang.org/x/sync/errgroup"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
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

// TestChatNotesToolReplay runs a batch of notes calls again with the same
// tool call IDs, as the next worker does after chatd stops in the middle of
// the batch. The replay returns the first results and changes no note.
func TestChatNotesToolReplay(t *testing.T) {
	t.Parallel()

	db, ps, rawDB := dbtestutil.NewDBWithSQLDB(t)
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkTokenBudget}))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)

	chat, err := server.CreateChat(ctx, CreateOptions{OrganizationID: org.ID, OwnerID: user.ID, Title: "notes", ModelConfigID: model.ID, InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")}})
	require.NoError(t, err)
	require.NoError(t, WaitUntilIdleForTest(ctx, server))
	tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
	require.NoError(t, err)

	type call struct {
		id   uuid.UUID
		args chatNotesArgs
	}
	batch := []call{
		{uuid.New(), chatNotesArgs{Action: "write", Path: "checkpoint.md", Content: "goal"}},
		{uuid.New(), chatNotesArgs{Action: "append", Path: "checkpoint.md", Content: "next"}},
		{uuid.New(), chatNotesArgs{Action: "append", Path: "log.md", Content: "one"}},
		{uuid.New(), chatNotesArgs{Action: "append", Path: "log.md", Content: "two"}},
		{uuid.New(), chatNotesArgs{Action: "write", Path: "tmp.md", Content: "x"}},
		{uuid.New(), chatNotesArgs{Action: "delete", Path: "tmp.md"}},
		{uuid.New(), chatNotesArgs{Action: "append", Path: "tmp.md", Content: "y"}},
	}
	runBatch := func() []string {
		results := make([]string, 0, len(batch))
		for _, c := range batch {
			results = append(results, runChatTodoTool(workspacesdk.WithToolCallID(ctx, c.id), t, tools, chatNotesToolName, c.args, false))
		}
		return results
	}
	read := func(path string) string {
		t.Helper()
		var note chatNoteResult
		require.NoError(t, json.Unmarshal([]byte(runChatTodoTool(ctx, t, tools, chatNotesToolName, chatNotesArgs{Action: "read", Path: path}, false)), &note))
		return note.Content
	}
	want := map[string]string{"checkpoint.md": "goal\nnext", "log.md": "one\ntwo", "tmp.md": "y"}

	first := runBatch()
	for path, content := range want {
		require.Equal(t, content, read(path), path)
	}
	require.JSONEq(t, `{"path": "tmp.md", "deleted": true}`, first[5])

	// Without the records, the appends would add their lines again, the
	// write of checkpoint.md would drop "next", and the delete would remove
	// the tmp.md that the last call wrote.
	require.Equal(t, first, runBatch())
	for path, content := range want {
		require.Equal(t, content, read(path), path)
	}

	// Two runs of one call at the same time, as when a takeover leaves the
	// old run going, append once and return the same result.
	raced := call{uuid.New(), chatNotesArgs{Action: "append", Path: "log.md", Content: "three"}}
	racedInput, err := json.Marshal(raced.args)
	require.NoError(t, err)
	notesTool := findToolByName(tools, chatNotesToolName)
	var eg errgroup.Group
	results := make([]fantasy.ToolResponse, 8)
	for i := range results {
		eg.Go(func() error {
			var err error
			results[i], err = notesTool.Run(workspacesdk.WithToolCallID(ctx, raced.id), fantasy.ToolCall{ID: uuid.NewString(), Name: chatNotesToolName, Input: string(racedInput)})
			return err
		})
	}
	require.NoError(t, eg.Wait())
	for _, result := range results {
		require.False(t, result.IsError, result.Content)
		require.JSONEq(t, results[0].Content, result.Content)
	}
	require.Equal(t, "one\ntwo\nthree", read("log.md"))

	// A call without a tool call ID records nothing and runs every time.
	runChatTodoTool(ctx, t, tools, chatNotesToolName, chatNotesArgs{Action: "append", Path: "log.md", Content: "four"}, false)
	runChatTodoTool(ctx, t, tools, chatNotesToolName, chatNotesArgs{Action: "append", Path: "log.md", Content: "four"}, false)
	require.Equal(t, "one\ntwo\nthree\nfour\nfour", read("log.md"))

	// A note change drops the chat's records older than a day.
	var records int
	require.NoError(t, rawDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_note_calls WHERE chat_id = $1`, chat.ID).Scan(&records))
	require.Equal(t, len(batch)+1, records)
	_, err = rawDB.ExecContext(ctx, `UPDATE chat_note_calls SET created_at = now() - interval '25 hours' WHERE chat_id = $1`, chat.ID)
	require.NoError(t, err)
	runChatTodoTool(workspacesdk.WithToolCallID(ctx, uuid.New()), t, tools, chatNotesToolName, chatNotesArgs{Action: "write", Path: "tmp.md", Content: "z"}, false)
	require.NoError(t, rawDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_note_calls WHERE chat_id = $1`, chat.ID).Scan(&records))
	require.Equal(t, 1, records)
}
