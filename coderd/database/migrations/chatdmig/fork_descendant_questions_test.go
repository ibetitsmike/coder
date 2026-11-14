package chatdmig_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/migrations"
	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

// TestDescendantQuestionsAnsweredBackfill runs the node that stores the
// answered state of descendant questions on a database with questions. It
// keeps every row, names the family root, and marks answered only the
// question that the old reads treated as answered.
func TestDescendantQuestionsAnsweredBackfill(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	g, err := migrations.LoadGraph()
	require.NoError(t, err)
	var node *chatdmig.Node
	for _, n := range g.Nodes {
		if strings.HasSuffix(n.Name, "_chat_descendant_questions_answered") {
			node = n
		}
	}
	require.NotNil(t, node)

	_, sqlDB := createDB(t, admin)
	require.NoError(t, migrations.UpGraph(sqlDB, withoutNode(g, node)))
	db := database.New(sqlDB)

	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	seed := database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: model.ID,
	}
	root := dbgen.Chat(t, db, seed)
	seed.ParentChatID = uuid.NullUUID{UUID: root.ID, Valid: true}
	seed.RootChatID = seed.ParentChatID
	pending := dbgen.Chat(t, db, seed)
	answered := dbgen.Chat(t, db, seed)

	asked := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	message := func(chatID uuid.UUID, at time.Time, visibility database.ChatMessageVisibility) {
		m := dbgen.ChatMessage(t, db, database.ChatMessage{
			ChatID:        chatID,
			ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
			Role:          database.ChatMessageRoleUser,
			Visibility:    visibility,
			Content:       pqtype.NullRawMessage{RawMessage: []byte(`[{"type": "text", "text": "Use Postgres"}]`), Valid: true},
		})
		_, err := sqlDB.Exec("UPDATE chat_messages SET created_at = $1 WHERE id = $2", at, m.ID)
		require.NoError(t, err)
	}
	for _, chat := range []database.Chat{root, pending, answered} {
		_, err := sqlDB.Exec(`INSERT INTO chat_descendant_questions (chat_id, tool_call_id, questions, created_at)
			VALUES ($1, 'call-1', '[{"header": "DB"}]', $2)`, chat.ID, asked)
		require.NoError(t, err)
	}
	// The pending question has a visible message from before it and a
	// model-only message after it. Neither answers it.
	message(pending.ID, asked.Add(-time.Minute), database.ChatMessageVisibilityBoth)
	message(pending.ID, asked.Add(time.Minute), database.ChatMessageVisibilityModel)
	message(answered.ID, asked.Add(time.Minute), database.ChatMessageVisibilityBoth)
	message(root.ID, asked.Add(time.Minute), database.ChatMessageVisibilityUser)

	require.NoError(t, migrations.UpAll(sqlDB))
	require.NoError(t, migrations.EnsureCleanAll(sqlDB))

	for _, tc := range []struct {
		chat       database.Chat
		answeredBy string // empty: the question stays pending
	}{
		{chat: root, answeredBy: "migrated"},
		{chat: pending},
		{chat: answered, answeredBy: "migrated"},
	} {
		var (
			rootID     uuid.UUID
			answeredAt sql.NullTime
			answeredBy sql.NullString
			createdAt  time.Time
		)
		err := sqlDB.QueryRow(`SELECT root_chat_id, answered_at, answered_by, created_at
			FROM chat_descendant_questions WHERE chat_id = $1`, tc.chat.ID).
			Scan(&rootID, &answeredAt, &answeredBy, &createdAt)
		require.NoError(t, err, "the node keeps the row of %s", tc.chat.ID)
		require.Equal(t, root.ID, rootID, "the root of the family")
		require.True(t, asked.Equal(createdAt))
		require.Equal(t, tc.answeredBy != "", answeredAt.Valid)
		require.Equal(t, tc.answeredBy, answeredBy.String)
	}
}
