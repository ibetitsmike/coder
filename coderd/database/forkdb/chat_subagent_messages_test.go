package forkdb_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestChatSubagentMessages(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	org := dbgen.Organization(t, db, database.Organization{})
	owner := dbgen.User(t, db, database.User{})
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	newChat := func() database.Chat {
		return dbgen.Chat(t, db, database.Chat{
			OrganizationID:    org.ID,
			OwnerID:           owner.ID,
			LastModelConfigID: model.ID,
		})
	}
	sender := newChat()
	recipient := newChat()

	now := time.Now().UTC().Truncate(time.Microsecond)
	oldID, newID := uuid.New(), uuid.New()
	for id, at := range map[uuid.UUID]time.Time{oldID: now.Add(-48 * time.Hour), newID: now} {
		require.NoError(t, fdb.InsertChatSubagentMessage(ctx, forkdb.InsertChatSubagentMessageParams{
			ID:              id,
			OrganizationID:  org.ID,
			SenderChatID:    sender.ID,
			RecipientChatID: recipient.ID,
			CreatedAt:       at,
		}))
	}

	got, err := fdb.GetChatSubagentMessageRecipient(ctx, forkdb.GetChatSubagentMessageRecipientParams{
		ID:             newID,
		OrganizationID: org.ID,
		SenderChatID:   sender.ID,
	})
	require.NoError(t, err)
	require.Equal(t, recipient.ID, got)

	// A call ID is scoped to its sender and its organization.
	_, err = fdb.GetChatSubagentMessageRecipient(ctx, forkdb.GetChatSubagentMessageRecipientParams{
		ID:             newID,
		OrganizationID: org.ID,
		SenderChatID:   recipient.ID,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = fdb.GetChatSubagentMessageRecipient(ctx, forkdb.GetChatSubagentMessageRecipientParams{
		ID:             newID,
		OrganizationID: uuid.New(),
		SenderChatID:   sender.ID,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)

	err = fdb.InsertChatSubagentMessage(ctx, forkdb.InsertChatSubagentMessageParams{
		ID:              newID,
		OrganizationID:  org.ID,
		SenderChatID:    sender.ID,
		RecipientChatID: recipient.ID,
		CreatedAt:       now,
	})
	require.True(t, database.IsUniqueViolation(err), "a call ID is recorded once: %v", err)

	// A delete in another organization keeps the rows.
	require.NoError(t, fdb.DeleteChatSubagentMessagesBefore(ctx, forkdb.DeleteChatSubagentMessagesBeforeParams{
		OrganizationID: uuid.New(),
		SenderChatID:   sender.ID,
		Before:         now.Add(-24 * time.Hour),
	}))
	_, err = fdb.GetChatSubagentMessageRecipient(ctx, forkdb.GetChatSubagentMessageRecipientParams{
		ID:             oldID,
		OrganizationID: org.ID,
		SenderChatID:   sender.ID,
	})
	require.NoError(t, err)

	require.NoError(t, fdb.DeleteChatSubagentMessagesBefore(ctx, forkdb.DeleteChatSubagentMessagesBeforeParams{
		OrganizationID: org.ID,
		SenderChatID:   sender.ID,
		Before:         now.Add(-24 * time.Hour),
	}))
	_, err = fdb.GetChatSubagentMessageRecipient(ctx, forkdb.GetChatSubagentMessageRecipientParams{
		ID:             oldID,
		OrganizationID: org.ID,
		SenderChatID:   sender.ID,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = fdb.GetChatSubagentMessageRecipient(ctx, forkdb.GetChatSubagentMessageRecipientParams{
		ID:             newID,
		OrganizationID: org.ID,
		SenderChatID:   sender.ID,
	})
	require.NoError(t, err)
}
