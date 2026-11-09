package chatd

// This file is fork code: it tests the interrupt cascade and the archive of
// an active family behind the fork-nested-subagents experiment.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// setStatus puts chat in status. A non-nil workerID marks the chat as owned,
// so that interrupting it stops in interrupting until the worker finishes.
func (f nestedSubagentsFixture) setStatus(t *testing.T, chat database.Chat, status database.ChatStatus, workerID uuid.NullUUID) {
	t.Helper()
	now := time.Now()
	_, err := f.db.UpdateChatStatus(chatdTestContext(t), database.UpdateChatStatusParams{
		ID:          chat.ID,
		Status:      status,
		WorkerID:    workerID,
		StartedAt:   sql.NullTime{Time: now, Valid: workerID.Valid},
		HeartbeatAt: sql.NullTime{Time: now, Valid: workerID.Valid},
	})
	require.NoError(t, err)
}

func (f nestedSubagentsFixture) requireStatus(t *testing.T, want database.ChatStatus, chats ...database.Chat) {
	t.Helper()
	for _, chat := range chats {
		got, err := f.db.GetChatByID(chatdTestContext(t), chat.ID)
		require.NoError(t, err)
		require.Equal(t, want, got.Status, "chat %q", chat.Title)
	}
}

func (f nestedSubagentsFixture) requireArchived(t *testing.T, want bool, chats ...database.Chat) {
	t.Helper()
	for _, chat := range chats {
		got, err := f.db.GetChatByID(chatdTestContext(t), chat.ID)
		require.NoError(t, err)
		require.Equal(t, want, got.Archived, "chat %q", chat.Title)
	}
}

func TestNestedInterruptCascade(t *testing.T) {
	t.Parallel()

	t.Run("Subtree", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
		ctx := chatdTestContext(t)

		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		sibling, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
		require.NoError(t, err)
		f.setStatus(t, f.root, database.ChatStatusWaiting, uuid.NullUUID{})
		f.requireStatus(t, database.ChatStatusRunning, child, sibling, grandchild)

		_, err = f.server.InterruptChat(ctx, child)
		require.NoError(t, err)
		f.requireStatus(t, database.ChatStatusWaiting, child, grandchild)
		f.requireStatus(t, database.ChatStatusRunning, sibling)

		// Stopping an idle root stops its running descendants.
		_, err = f.server.InterruptChat(ctx, f.root)
		require.NoError(t, err)
		f.requireStatus(t, database.ChatStatusWaiting, sibling)

		_, err = f.server.InterruptChat(ctx, f.root)
		require.ErrorIs(t, err, chatstate.ErrTransitionNotAllowed)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, upstreamExperimentsKnown(), Limits{})
		ctx := chatdTestContext(t)

		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)

		_, err = f.server.InterruptChat(ctx, f.root)
		require.NoError(t, err)
		f.requireStatus(t, database.ChatStatusWaiting, f.root)
		f.requireStatus(t, database.ChatStatusRunning, child)

		_, err = f.server.InterruptChat(ctx, f.root)
		require.ErrorIs(t, err, chatstate.ErrTransitionNotAllowed)
	})
}

func TestNestedArchiveActiveFamily(t *testing.T) {
	t.Parallel()

	nested := codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}
	owned := uuid.NullUUID{UUID: uuid.New(), Valid: true}

	// activeFamily returns the root of f, an idle child and a grandchild that
	// a worker runs.
	activeFamily := func(t *testing.T, f nestedSubagentsFixture) (database.Chat, database.Chat) {
		t.Helper()
		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
		require.NoError(t, err)
		f.setStatus(t, f.root, database.ChatStatusWaiting, uuid.NullUUID{})
		f.setStatus(t, child, database.ChatStatusWaiting, uuid.NullUUID{})
		f.setStatus(t, grandchild, database.ChatStatusRunning, owned)
		return child, grandchild
	}

	t.Run("Settles", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, nested, Limits{})
		ctx := chatdTestContext(t)
		child, grandchild := activeFamily(t, f)

		// Simulate the worker of the grandchild finishing its interruption.
		workerErr := make(chan error, 1)
		go func() {
			testutil.Eventually(ctx, t, func(ctx context.Context) bool {
				got, err := f.db.GetChatByID(ctx, grandchild.ID)
				return err == nil && got.Status == database.ChatStatusInterrupting
			}, testutil.IntervalFast)
			workerErr <- chatstate.NewChatMachine(f.db, f.server.pubsub, grandchild.ID).Update(ctx, func(tx *chatstate.Tx, _ database.Store) error {
				_, err := tx.FinishInterruption(chatstate.FinishInterruptionInput{})
				return err
			})
		}()

		require.NoError(t, f.server.ArchiveChat(ctx, f.root))
		require.NoError(t, testutil.RequireReceive(ctx, t, workerErr))
		f.requireArchived(t, true, f.root, child, grandchild)
	})

	t.Run("Timeout", func(t *testing.T) {
		t.Parallel()
		mClock := quartz.NewMock(t)
		f := newNestedSubagentsFixture(t, nested, Limits{}, withInternalTestServerClock(mClock))
		ctx := chatdTestContext(t)
		child, grandchild := activeFamily(t, f)

		timerTrap := mClock.Trap().NewTimer("chatd", "family_archive_settle")
		defer timerTrap.Close()
		tickerTrap := mClock.Trap().NewTicker("chatd", "family_archive_poll")
		defer tickerTrap.Close()

		archiveErr := make(chan error, 1)
		go func() {
			archiveErr <- f.server.ArchiveChat(ctx, f.root)
		}()
		timerTrap.MustWait(ctx).MustRelease(ctx)
		tickerTrap.MustWait(ctx).MustRelease(ctx)
		for left := familyArchiveSettleTimeout; left > 0; left -= subagentAwaitFallbackPoll {
			mClock.Advance(min(left, subagentAwaitFallbackPoll)).MustWait(ctx)
		}

		err := testutil.RequireReceive(ctx, t, archiveErr)
		require.ErrorIs(t, err, chatstate.ErrTransitionNotAllowed)
		f.requireStatus(t, database.ChatStatusInterrupting, grandchild)
		f.requireArchived(t, false, f.root, child, grandchild)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, upstreamExperimentsKnown(), Limits{})
		ctx := chatdTestContext(t)

		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		f.setStatus(t, f.root, database.ChatStatusWaiting, uuid.NullUUID{})
		f.setStatus(t, child, database.ChatStatusRunning, owned)

		err = f.server.ArchiveChat(ctx, f.root)
		require.ErrorIs(t, err, chatstate.ErrTransitionNotAllowed)
		f.requireStatus(t, database.ChatStatusRunning, child)
		f.requireArchived(t, false, f.root, child)
	})
}
