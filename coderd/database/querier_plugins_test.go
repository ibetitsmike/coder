package database_test

import (
	"crypto/sha256"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/testutil"
)

func TestPluginChatBindings(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	ctx := testutil.Context(t, testutil.WaitMedium)

	org := dbgen.Organization(t, db, database.Organization{})
	otherOrg := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	otherUser := dbgen.User(t, db, database.User{})
	modelCfg := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
	plugin := dbgen.Plugin(t, db, database.Plugin{
		OrganizationID: org.ID,
		FileID:         file.ID,
		CreatedBy:      user.ID,
		Enabled:        true,
	})

	newChat := func(orgID, ownerID uuid.UUID, parent uuid.NullUUID) database.Chat {
		return dbgen.Chat(t, db, database.Chat{
			OrganizationID:    orgID,
			OwnerID:           ownerID,
			LastModelConfigID: modelCfg.ID,
			ParentChatID:      parent,
			RootChatID:        parent,
		})
	}
	addMessage := func(chatID uuid.UUID, role database.ChatMessageRole) database.ChatMessage {
		return dbgen.ChatMessage(t, db, database.ChatMessage{ChatID: chatID, Role: role})
	}

	// Chats that exist before the link is created.
	oldChat := newChat(org.ID, user.ID, uuid.NullUUID{})
	addMessage(oldChat.ID, database.ChatMessageRoleUser)
	oldLast := addMessage(oldChat.ID, database.ChatMessageRoleAssistant)
	archivedChat := newChat(org.ID, user.ID, uuid.NullUUID{})
	_, err := db.ArchiveChatByID(ctx, archivedChat.ID)
	require.NoError(t, err)
	otherOrgChat := newChat(otherOrg.ID, user.ID, uuid.NullUUID{})
	unlinkedUserChat := newChat(org.ID, otherUser.ID, uuid.NullUUID{})

	_, err = db.UpsertPluginUserLink(ctx, database.UpsertPluginUserLinkParams{
		PluginID:   plugin.ID,
		ExternalID: "+15550100",
		UserID:     user.ID,
	})
	require.NoError(t, err)

	// Chats created after the link.
	freshChat := newChat(org.ID, user.ID, uuid.NullUUID{})
	childChat := newChat(org.ID, user.ID, uuid.NullUUID{UUID: freshChat.ID, Valid: true})

	// Running twice must not reset existing cursors.
	require.NoError(t, db.EnsurePluginChatBindings(ctx, plugin.ID))
	require.NoError(t, db.EnsurePluginChatBindings(ctx, plugin.ID))

	type binding struct {
		sentMessageID int64
		sentStatus    database.NullChatStatus
	}
	rows, err := sqlDB.QueryContext(ctx,
		"SELECT chat_id, sent_message_id, sent_status FROM plugin_chat_bindings WHERE plugin_id = $1", plugin.ID)
	require.NoError(t, err)
	got := map[uuid.UUID]binding{}
	for rows.Next() {
		var chatID uuid.UUID
		var b binding
		require.NoError(t, rows.Scan(&chatID, &b.sentMessageID, &b.sentStatus))
		got[chatID] = b
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	require.Equal(t, map[uuid.UUID]binding{
		oldChat.ID: {
			sentMessageID: oldLast.ID,
			sentStatus:    database.NullChatStatus{ChatStatus: database.ChatStatusWaiting, Valid: true},
		},
		freshChat.ID: {sentMessageID: 0},
	}, got, "only top-level, unarchived chats of linked users in the plugin org are bound")
	for _, id := range []uuid.UUID{archivedChat.ID, otherOrgChat.ID, unlinkedUserChat.ID, childChat.ID} {
		require.NotContains(t, got, id)
	}

	dueChatIDs := func() []uuid.UUID {
		due, err := db.GetPluginChatBindingsDue(ctx, database.GetPluginChatBindingsDueParams{
			PluginID:   plugin.ID,
			LimitCount: 100,
		})
		require.NoError(t, err)
		ids := make([]uuid.UUID, 0, len(due))
		for _, row := range due {
			ids = append(ids, row.ChatID)
		}
		return ids
	}

	// The fresh chat has an undelivered status. The old chat has nothing new.
	require.ElementsMatch(t, []uuid.UUID{freshChat.ID}, dueChatIDs())

	// A user-visible assistant reply makes the old chat due again.
	newReply := addMessage(oldChat.ID, database.ChatMessageRoleAssistant)
	require.ElementsMatch(t, []uuid.UUID{freshChat.ID, oldChat.ID}, dueChatIDs())

	msgs, err := db.GetPluginChatMessagesAfter(ctx, database.GetPluginChatMessagesAfterParams{
		ChatID:     oldChat.ID,
		AfterID:    oldLast.ID,
		LimitCount: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, newReply.ID, msgs[0].ID)

	// A running chat is never due, even with undelivered messages.
	_, err = db.UpdateChatStatus(ctx, database.UpdateChatStatusParams{ID: oldChat.ID, Status: database.ChatStatusRunning})
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{freshChat.ID}, dueChatIDs())

	// Delivering the fresh chat's status clears it.
	require.NoError(t, db.UpdatePluginChatBindingDelivered(ctx, database.UpdatePluginChatBindingDeliveredParams{
		PluginID:      plugin.ID,
		ChatID:        freshChat.ID,
		SentMessageID: 0,
		SentStatus:    database.NullChatStatus{ChatStatus: database.ChatStatusWaiting, Valid: true},
	}))
	require.Empty(t, dueChatIDs())
}

func TestPluginLinkCodes(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	ctx := testutil.Context(t, testutil.WaitShort)

	org := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
	plugin := dbgen.Plugin(t, db, database.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID})

	validHash := sha256.Sum256([]byte("valid-code"))
	require.NoError(t, db.InsertPluginLinkCode(ctx, database.InsertPluginLinkCodeParams{
		CodeHash:  validHash[:],
		PluginID:  plugin.ID,
		UserID:    user.ID,
		ExpiresAt: dbtime.Now().Add(time.Hour),
	}))

	consumed, err := db.ConsumePluginLinkCode(ctx, database.ConsumePluginLinkCodeParams{PluginID: plugin.ID, CodeHash: validHash[:]})
	require.NoError(t, err)
	require.Equal(t, user.ID, consumed.UserID)

	_, err = db.ConsumePluginLinkCode(ctx, database.ConsumePluginLinkCodeParams{PluginID: plugin.ID, CodeHash: validHash[:]})
	require.ErrorIs(t, err, sql.ErrNoRows, "a code can be consumed only once")

	expiredHash := sha256.Sum256([]byte("expired-code"))
	require.NoError(t, db.InsertPluginLinkCode(ctx, database.InsertPluginLinkCodeParams{
		CodeHash:  expiredHash[:],
		PluginID:  plugin.ID,
		UserID:    user.ID,
		ExpiresAt: dbtime.Now().Add(-time.Minute),
	}))
	_, err = db.ConsumePluginLinkCode(ctx, database.ConsumePluginLinkCodeParams{PluginID: plugin.ID, CodeHash: expiredHash[:]})
	require.ErrorIs(t, err, sql.ErrNoRows, "an expired code cannot be consumed")

	require.NoError(t, db.DeleteExpiredPluginLinkCodes(ctx))
	var remaining int
	require.NoError(t, sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM plugin_link_codes WHERE plugin_id = $1", plugin.ID).Scan(&remaining))
	require.Zero(t, remaining)
}

func TestPluginLeases(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitShort)

	org := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
	enabled := dbgen.Plugin(t, db, database.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID, Enabled: true})
	dbgen.Plugin(t, db, database.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID, Enabled: false})

	runnerA, runnerB := uuid.New(), uuid.New()
	acquire := func(runner uuid.UUID) []database.Plugin {
		plugins, err := db.AcquirePluginLeases(ctx, database.AcquirePluginLeasesParams{
			RunnerID:     runner,
			LeaseSeconds: 60,
			MaxPlugins:   10,
		})
		require.NoError(t, err)
		return plugins
	}

	leased := acquire(runnerA)
	require.Len(t, leased, 1, "only enabled plugins are leased")
	require.Equal(t, enabled.ID, leased[0].ID)
	require.Equal(t, runnerA, leased[0].RunnerID.UUID)

	require.Empty(t, acquire(runnerB), "a live lease is not stolen")
	require.Len(t, acquire(runnerA), 1, "the holder renews its lease")

	require.NoError(t, db.ReleasePluginLeases(ctx, runnerA))
	leased = acquire(runnerB)
	require.Len(t, leased, 1)
	require.Equal(t, runnerB, leased[0].RunnerID.UUID)
}
