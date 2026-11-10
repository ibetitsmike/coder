package chatd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/agenthooks"
	"github.com/coder/coder/v2/testutil"
)

// A user_prompt_submit hook sees the /loop command as submitted, so it can
// deny or audit scheduled execution. The chat stores only the prompt, and a
// prompt the hook puts in place of the submitted one is the prompt of every
// run.
func TestLoopCommandPromptHook(t *testing.T) {
	t.Parallel()
	db, ps := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	user, org, model := seedChatDependencies(t, db)
	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    org.ID,
		OwnerID:           user.ID,
		LastModelConfigID: model.ID,
	})
	var received agenthooks.Request
	consumer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		_, err := w.Write([]byte(`{"permission":{"decision":"allow","input_override":{"prompt":"check CI quietly"}}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(consumer.Close)
	server := newTestServer(t, db, ps, uuid.New(), func(cfg *chatd.Config) {
		cfg.HookDispatcher = newHookDispatcher(t, db, consumer)
		cfg.Experiments = append(slices.Clone(codersdk.ExperimentsKnown), codersdk.ExperimentForkChatWakeups)
	})

	result, err := server.SendMessage(ctx, chatd.SendMessageOptions{
		ChatID:  chat.ID,
		Content: []codersdk.ChatMessagePart{codersdk.ChatMessageText("/loop 5m check CI")},
	})
	require.NoError(t, err)
	require.Equal(t, "check CI quietly", hookMessageText(t, result.Message))
	require.Equal(t, agenthooks.EventUserPromptSubmit, received.Type)
	require.Equal(t, "/loop 5m check CI", decodeHookData[agenthooks.UserPromptSubmitData](t, received).Prompt)
	wakeups := loopWakeups(t, db, chat.ID)
	require.Len(t, wakeups, 1)
	require.Equal(t, "check CI quietly", wakeups[0].Prompt)
}

// The first message of a new chat is a /loop command too, except in a
// subagent, whose first message its parent's model wrote.
func TestLoopCommandCreateChat(t *testing.T) {
	t.Parallel()
	db, ps := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	user, org, model := seedChatDependencies(t, db)
	server := newTestServer(t, db, ps, uuid.New(), func(cfg *chatd.Config) {
		cfg.Experiments = append(slices.Clone(codersdk.ExperimentsKnown), codersdk.ExperimentForkChatWakeups)
	})
	create := func(text string, parent uuid.NullUUID) (database.Chat, error) {
		return server.CreateChat(ctx, chatd.CreateOptions{
			OrganizationID:     org.ID,
			OwnerID:            user.ID,
			ParentChatID:       parent,
			RootChatID:         parent,
			Title:              text,
			ModelConfigID:      model.ID,
			InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText(text)},
		})
	}

	chat, err := create("/loop 5m check CI", uuid.NullUUID{})
	require.NoError(t, err)
	wakeups := loopWakeups(t, db, chat.ID)
	require.Len(t, wakeups, 1)
	require.Equal(t, "check CI", wakeups[0].Prompt)
	require.EqualValues(t, 300, wakeups[0].IntervalSeconds.Int32)
	require.Equal(t, user.ID, wakeups[0].CreatedBy)
	require.Equal(t, "check CI", chat.Title, "the fallback title comes from the loop prompt")

	_, err = create("/loop 30s check CI", uuid.NullUUID{})
	var invalid *chatd.InvalidLoopCommandError
	require.ErrorAs(t, err, &invalid, "an invalid loop refuses the chat")
	require.Contains(t, invalid.Reason, "interval must be between")

	child, err := create("/loop 5m check CI", uuid.NullUUID{UUID: chat.ID, Valid: true})
	require.NoError(t, err)
	require.Empty(t, loopWakeups(t, db, child.ID))
}

func loopWakeups(t *testing.T, db database.Store, chatID uuid.UUID) []forkdb.ChatWakeup {
	t.Helper()
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	rows, err := fdb.ListChatWakeupsByChatID(testutil.Context(t, testutil.WaitShort), chatID)
	require.NoError(t, err)
	return rows
}
