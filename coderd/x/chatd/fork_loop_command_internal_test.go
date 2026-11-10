package chatd

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

func TestParseLoopCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		text         string
		wantCommand  bool
		wantInterval int
		wantPrompt   string
		wantErr      string
	}{
		{name: "Interval", text: "/loop 5m check CI", wantCommand: true, wantInterval: 300, wantPrompt: "check CI"},
		{name: "DefaultInterval", text: "/loop check CI", wantCommand: true, wantInterval: 600, wantPrompt: "check CI"},
		{name: "Days", text: "  /loop 1d\nsummarize the day  ", wantCommand: true, wantInterval: 86400, wantPrompt: "summarize the day"},
		{name: "BelowMinimum", text: "/loop 30s x", wantCommand: true, wantErr: "interval must be between"},
		{name: "AboveMaximum", text: "/loop 31d x", wantCommand: true, wantErr: "interval must be between"},
		{name: "NoPrompt", text: "/loop", wantCommand: true, wantErr: "usage: /loop [interval] <prompt>"},
		{name: "IntervalWithoutPrompt", text: "/loop 5m ", wantCommand: true, wantErr: "usage: /loop [interval] <prompt>"},
		{name: "NestedLoop", text: "/loop 5m /loop 1m x", wantCommand: true, wantErr: "must not start with /loop"},
		{name: "OtherCommand", text: "/loopy x"},
		{name: "NotAtStart", text: "hello /loop 5m x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec, ok, err := parseLoopCommand(tc.text)
			require.Equal(t, tc.wantCommand, ok)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if !tc.wantCommand {
				return
			}
			require.Equal(t, forkdb.ChatWakeupKindInterval, spec.kind)
			require.Equal(t, wakeupSourceLoop, spec.source)
			require.Equal(t, tc.wantInterval, spec.intervalSeconds)
			require.Equal(t, tc.wantPrompt, spec.prompt)
		})
	}
}

// A /loop message runs its prompt now and schedules it in the transaction
// that stores the message, so a refused send leaves no wake-up behind.
func TestLoopCommandSendMessage(t *testing.T) {
	t.Parallel()
	const prompt = "check CI"
	newServer := func(t *testing.T, f *workerTestFixture, experiments codersdk.Experiments) *Server {
		t.Helper()
		mClock := quartz.NewMock(t)
		mClock.Set(wakeupTestSlot)
		server := newUnstartedServer(t, f.pubsub, f.db, withInternalTestServerClock(mClock))
		server.experiments = experiments
		return server
	}
	send := func(t *testing.T, server *Server, chat database.Chat, sender uuid.UUID) error {
		t.Helper()
		_, err := server.SendMessage(chatdTestContext(t), SendMessageOptions{
			ChatID:    chat.ID,
			CreatedBy: sender,
			Content:   []codersdk.ChatMessagePart{codersdk.ChatMessageText("/loop 5m " + prompt)},
		})
		return err
	}
	wakeups := func(t *testing.T, f *workerTestFixture, chat database.Chat) []forkdb.ChatWakeup {
		t.Helper()
		fdb, err := forkdb.From(f.db)
		require.NoError(t, err)
		rows, err := fdb.ListChatWakeupsByChatID(testutil.Context(t, testutil.WaitShort), chat.ID)
		require.NoError(t, err)
		return rows
	}

	t.Run("Schedules", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		chat := f.createWakeupChat(t)
		server := newServer(t, f, codersdk.Experiments{codersdk.ExperimentForkChatWakeups})

		require.NoError(t, send(t, server, chat, chat.OwnerID))
		require.Equal(t, 1, f.countPromptMessages(t, chat, prompt), "the first run is the sent message")
		rows := wakeups(t, f, chat)
		require.Len(t, rows, 1)
		got := rows[0]
		require.Equal(t, forkdb.ChatWakeupKindInterval, got.Kind)
		require.EqualValues(t, 300, got.IntervalSeconds.Int32)
		require.Equal(t, wakeupSourceLoop, got.Source)
		require.Equal(t, prompt, got.Prompt)
		require.Equal(t, chat.OwnerID, got.CreatedBy)
		require.Equal(t, chat.OrganizationID, got.OrganizationID)
		require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
		require.True(t, wakeupTestSlot.Add(5*time.Minute).Equal(got.NextRunAt.Time), "next run %s", got.NextRunAt.Time)
	})

	t.Run("SentByAnotherUser", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		chat := f.createWakeupChat(t)
		sender := dbgen.User(t, f.db, database.User{})
		server := newServer(t, f, codersdk.Experiments{codersdk.ExperimentForkChatWakeups})

		require.NoError(t, send(t, server, chat, sender.ID))
		rows := wakeups(t, f, chat)
		require.Len(t, rows, 1)
		require.Equal(t, sender.ID, rows[0].CreatedBy, "the loop belongs to the user who sent it")
	})

	t.Run("ArchivedChat", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		chat := f.createWakeupChat(t)
		forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, true)
		server := newServer(t, f, codersdk.Experiments{codersdk.ExperimentForkChatWakeups})

		require.ErrorIs(t, send(t, server, chat, chat.OwnerID), ErrChatArchived)
		require.Empty(t, wakeups(t, f, chat))
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		chat := f.createWakeupChat(t)
		server := newServer(t, f, upstreamExperimentsKnown())

		require.NoError(t, send(t, server, chat, chat.OwnerID))
		require.Equal(t, 1, f.countPromptMessages(t, chat, "/loop 5m "+prompt))
		require.Empty(t, wakeups(t, f, chat))
	})
}
