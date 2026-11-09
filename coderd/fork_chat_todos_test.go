package coderd_test

// This file is fork code: it covers the chat todo list routes of the
// fork-chat-todos experiment.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	dbpubsub "github.com/coder/coder/v2/coderd/database/pubsub"
	"github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

// newForkChatTodosAPI starts coderd with the fork-chat-todos experiment
// enabled. It returns the unwrapped store and pubsub so a test can write a
// todo list the way the chat daemon does.
func newForkChatTodosAPI(t *testing.T, enabled bool) (*codersdk.Client, database.Store, dbpubsub.Pubsub) {
	t.Helper()

	db, ps := dbtestutil.NewDB(t)
	values := coderdtest.DeploymentValues(t)
	if enabled {
		values.Experiments = serpent.StringArray{string(codersdk.ExperimentForkChatTodos)}
	}

	client, _, _ := coderdtest.NewWithAPI(t, &coderdtest.Options{
		DeploymentValues: values,
		Database:         db,
		Pubsub:           ps,
	})
	return client, db, ps
}

// newForkChatTodosChat returns a chat owned by the first user, which is the
// only member of its organization.
func newForkChatTodosChat(t testing.TB, db database.Store, user codersdk.CreateFirstUserResponse) database.Chat {
	t.Helper()

	return dbgen.Chat(t, db, database.Chat{
		OrganizationID: user.OrganizationID,
		OwnerID:        user.UserID,
		LastModelConfigID: dbgen.ChatModelConfig(t, db, database.ChatModelConfig{
			OrganizationID: user.OrganizationID,
		}).ID,
	})
}

// writeForkChatTodos stores a todo list and publishes the event the chat
// daemon publishes with it, which is how a watcher learns of the new version.
func writeForkChatTodos(
	ctx context.Context,
	t testing.TB,
	db database.Store,
	ps dbpubsub.Pubsub,
	chatID uuid.UUID,
	items string,
) int64 {
	t.Helper()

	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	row, err := fdb.UpsertChatTodos(ctx, forkdb.UpsertChatTodosParams{
		ChatID:    chatID,
		Items:     json.RawMessage(items),
		UpdatedAt: dbtime.Now(),
	})
	require.NoError(t, err)

	payload, err := json.Marshal(pubsub.ChatTodosEvent{ChatID: chatID, Version: row.Version})
	require.NoError(t, err)
	require.NoError(t, ps.Publish(pubsub.ChatTodosEventChannel(chatID), payload))
	return row.Version
}

// TestForkChatTodosExperimentOff checks that a fork path stays closed while
// its experiment is disabled.
func TestForkChatTodosExperimentOff(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db, _ := newForkChatTodosAPI(t, false)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)

	for _, path := range []string{
		"/api/v2/chats/fork/" + chat.ID.String() + "/todos",
		"/api/v2/chats/fork/" + chat.ID.String() + "/todos/watch",
	} {
		res, err := client.Request(ctx, http.MethodGet, path, nil)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode, path)
	}
}

// TestForkChatTodos checks reading the todo list of a chat the caller owns.
func TestForkChatTodos(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db, ps := newForkChatTodosAPI(t, true)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)

	// A fork path with no route answers 404 even with the experiment on: the
	// fork routes take their middleware inline rather than from a mounted
	// {chat} subrouter, which would answer every fork path.
	res, err := client.Request(ctx, http.MethodGet, "/api/v2/chats/fork/anything", nil)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	empty, err := client.ChatTodos(ctx, chat.ID)
	require.NoError(t, err)
	require.Empty(t, empty.Todos)
	require.Zero(t, empty.Version)

	// The list arrives as an empty array, so a client never reads null.
	res, err = client.Request(ctx, http.MethodGet, "/api/v2/chats/fork/"+chat.ID.String()+"/todos", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var raw struct {
		Todos *[]codersdk.ChatTodo `json:"todos"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&raw))
	require.NotNil(t, raw.Todos)

	version := writeForkChatTodos(ctx, t, db, ps, chat.ID,
		`[{"id": "one", "content": "plan the work", "status": "in_progress"}]`)

	got, err := client.ChatTodos(ctx, chat.ID)
	require.NoError(t, err)
	require.Equal(t, version, got.Version)
	require.Equal(t, []codersdk.ChatTodo{{
		ID:      "one",
		Content: "plan the work",
		Status:  codersdk.ChatTodoStatusInProgress,
	}}, got.Todos)
}

// TestForkChatTodosPermission checks that a member of the same organization
// cannot read a todo list of a chat it may not read.
func TestForkChatTodosPermission(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db, ps := newForkChatTodosAPI(t, true)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)
	writeForkChatTodos(ctx, t, db, ps, chat.ID,
		`[{"id": "one", "content": "plan the work", "status": "pending"}]`)

	other, _ := coderdtest.CreateAnotherUser(t, client, user.OrganizationID)
	for _, path := range []string{
		"/api/v2/chats/fork/" + chat.ID.String() + "/todos",
		"/api/v2/chats/fork/" + chat.ID.String() + "/todos/watch",
	} {
		res, err := other.Request(ctx, http.MethodGet, path, nil)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, path)
	}
}

// TestForkChatTodosWatch checks that a watcher receives the current list and
// then the list of every later write.
func TestForkChatTodosWatch(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db, ps := newForkChatTodosAPI(t, true)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)

	events, closer, err := client.WatchChatTodos(ctx, chat.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	initial := testutil.RequireReceive(ctx, t, events)
	require.Empty(t, initial.Todos)
	require.Zero(t, initial.Version)

	// The subscription is active before the websocket is accepted, so a write
	// that lands right after Dial returns is not lost.
	version := writeForkChatTodos(ctx, t, db, ps, chat.ID,
		`[{"id": "one", "content": "plan the work", "status": "in_progress"}]`)
	first := testutil.RequireReceive(ctx, t, events)
	require.Equal(t, version, first.Version)
	require.Len(t, first.Todos, 1)

	version = writeForkChatTodos(ctx, t, db, ps, chat.ID,
		`[{"id": "one", "content": "plan the work", "status": "done"},`+
			`{"id": "two", "content": "ship it", "status": "pending"}]`)
	second := testutil.RequireReceive(ctx, t, events)
	require.Equal(t, version, second.Version)
	require.Len(t, second.Todos, 2)
	require.Equal(t, codersdk.ChatTodoStatusDone, second.Todos[0].Status)
}
