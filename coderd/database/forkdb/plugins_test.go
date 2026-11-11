package forkdb_test

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
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestPluginChatBindings(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitMedium)

	org := dbgen.Organization(t, db, database.Organization{})
	otherOrg := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	otherUser := dbgen.User(t, db, database.User{})
	modelCfg := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
	plugin := dbgen.Plugin(t, db, forkdb.Plugin{
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

	_, err = fdb.UpsertPluginUserLink(ctx, forkdb.UpsertPluginUserLinkParams{
		PluginID:   plugin.ID,
		ExternalID: "+15550100",
		UserID:     user.ID,
	})
	require.NoError(t, err)

	// Chats created after the link.
	freshChat := newChat(org.ID, user.ID, uuid.NullUUID{})
	childChat := newChat(org.ID, user.ID, uuid.NullUUID{UUID: freshChat.ID, Valid: true})

	// Running twice must not reset existing cursors.
	require.NoError(t, fdb.EnsurePluginChatBindings(ctx, plugin.ID))
	require.NoError(t, fdb.EnsurePluginChatBindings(ctx, plugin.ID))

	type binding struct {
		sentMessageID int64
		sentStatus    database.NullChatStatus
	}
	rows, err := sqlDB.QueryContext(ctx,
		"SELECT chat_id, sent_message_id, sent_status FROM chatd.plugin_chat_bindings WHERE plugin_id = $1", plugin.ID)
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
		due, err := fdb.GetPluginChatBindingsDue(ctx, forkdb.GetPluginChatBindingsDueParams{
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

	// Delivery reads the reply with upstream's paginated query, after the
	// cursor of the binding.
	msgs, err := db.GetChatMessagesByChatIDAscPaginated(ctx, database.GetChatMessagesByChatIDAscPaginatedParams{
		ChatID:   oldChat.ID,
		AfterID:  oldLast.ID,
		LimitVal: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, newReply.ID, msgs[0].ID)

	// A running chat is never due, even with undelivered messages.
	_, err = db.UpdateChatStatus(ctx, database.UpdateChatStatusParams{ID: oldChat.ID, Status: database.ChatStatusRunning})
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{freshChat.ID}, dueChatIDs())

	freshDelivered := func() bool {
		due, err := fdb.GetPluginChatBindingsDue(ctx, forkdb.GetPluginChatBindingsDueParams{
			PluginID:   plugin.ID,
			LimitCount: 100,
		})
		require.NoError(t, err)
		require.Len(t, due, 1)
		require.Equal(t, freshChat.ID, due[0].ChatID)
		return due[0].Delivered
	}
	require.False(t, freshDelivered(), "a new binding has not reached a link")

	// Delivering the fresh chat's status clears it.
	require.NoError(t, fdb.UpdatePluginChatBindingDelivered(ctx, forkdb.UpdatePluginChatBindingDeliveredParams{
		PluginID:      plugin.ID,
		ChatID:        freshChat.ID,
		SentMessageID: 0,
		SentStatus:    database.NullChatStatus{ChatStatus: database.ChatStatusWaiting, Valid: true},
		Delivered:     true,
	}))
	require.Empty(t, dueChatIDs())

	// Once a link got the chat, a later cursor update that reached no link
	// keeps the mark.
	_, err = db.UpdateChatStatus(ctx, database.UpdateChatStatusParams{ID: freshChat.ID, Status: database.ChatStatusError})
	require.NoError(t, err)
	require.True(t, freshDelivered())
	require.NoError(t, fdb.UpdatePluginChatBindingDelivered(ctx, forkdb.UpdatePluginChatBindingDeliveredParams{
		PluginID:      plugin.ID,
		ChatID:        freshChat.ID,
		SentMessageID: 0,
		SentStatus:    database.NullChatStatus{ChatStatus: database.ChatStatusError, Valid: true},
	}))
	_, err = db.UpdateChatStatus(ctx, database.UpdateChatStatusParams{ID: freshChat.ID, Status: database.ChatStatusWaiting})
	require.NoError(t, err)
	require.True(t, freshDelivered())
}

func TestPluginLinkCodes(t *testing.T) {
	t.Parallel()

	db, _, _ := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	org := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
	plugin := dbgen.Plugin(t, db, forkdb.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID})

	validHash := sha256.Sum256([]byte("valid-code"))
	require.NoError(t, fdb.InsertPluginLinkCode(ctx, forkdb.InsertPluginLinkCodeParams{
		CodeHash:  validHash[:],
		PluginID:  plugin.ID,
		UserID:    user.ID,
		ExpiresAt: dbtime.Now().Add(time.Hour),
	}))

	consumed, err := fdb.ConsumePluginLinkCode(ctx, forkdb.ConsumePluginLinkCodeParams{PluginID: plugin.ID, CodeHash: validHash[:]})
	require.NoError(t, err)
	require.Equal(t, user.ID, consumed.UserID)

	_, err = fdb.ConsumePluginLinkCode(ctx, forkdb.ConsumePluginLinkCodeParams{PluginID: plugin.ID, CodeHash: validHash[:]})
	require.ErrorIs(t, err, sql.ErrNoRows, "a code can be consumed only once")

	expiredHash := sha256.Sum256([]byte("expired-code"))
	require.NoError(t, fdb.InsertPluginLinkCode(ctx, forkdb.InsertPluginLinkCodeParams{
		CodeHash:  expiredHash[:],
		PluginID:  plugin.ID,
		UserID:    user.ID,
		ExpiresAt: dbtime.Now().Add(-time.Minute),
	}))
	_, err = fdb.ConsumePluginLinkCode(ctx, forkdb.ConsumePluginLinkCodeParams{PluginID: plugin.ID, CodeHash: expiredHash[:]})
	require.ErrorIs(t, err, sql.ErrNoRows, "an expired code cannot be consumed")
}

// TestPluginUserLinkActiveChatUnique checks that two links of one plugin
// cannot follow the same chat: delivery sends a chat's events to the link
// that follows it, so a shared pointer would leak one identity's
// conversation to another.
func TestPluginUserLinkActiveChatUnique(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	org := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
	modelCfg := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	plugin := dbgen.Plugin(t, db, forkdb.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID})
	otherPlugin := dbgen.Plugin(t, db, forkdb.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID})
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: modelCfg.ID})

	setActive := func(pluginID uuid.UUID, externalID string) error {
		_, err := fdb.UpsertPluginUserLink(ctx, forkdb.UpsertPluginUserLinkParams{PluginID: pluginID, ExternalID: externalID, UserID: user.ID})
		require.NoError(t, err)
		return fdb.SetPluginUserLinkActiveChat(ctx, forkdb.SetPluginUserLinkActiveChatParams{
			PluginID:     pluginID,
			ExternalID:   externalID,
			ActiveChatID: uuid.NullUUID{UUID: chat.ID, Valid: true},
		})
	}
	require.NoError(t, setActive(plugin.ID, "+15550101"))
	require.Error(t, setActive(plugin.ID, "+15550102"), "a second link of the plugin cannot follow the chat")
	require.NoError(t, setActive(otherPlugin.ID, "+15550101"), "links of another plugin are independent")
}

func TestPluginLeases(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	org := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
	enabled := dbgen.Plugin(t, db, forkdb.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID, Enabled: true})
	dbgen.Plugin(t, db, forkdb.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID, Enabled: false})

	runnerA, runnerB := uuid.New(), uuid.New()
	acquire := func(runner uuid.UUID) []forkdb.Plugin {
		plugins, err := fdb.AcquirePluginLeases(ctx, forkdb.AcquirePluginLeasesParams{
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

	require.NoError(t, fdb.ReleasePluginLeases(ctx, runnerA))
	leased = acquire(runnerB)
	require.Len(t, leased, 1)
	require.Equal(t, runnerB, leased[0].RunnerID.UUID)
}

func mustFrom(t *testing.T, db database.Store) forkdb.Store {
	t.Helper()
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	return fdb
}
