package chatdmig_test

import (
	"database/sql"
	"encoding/json"
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
	asked := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	// A chat without a root names itself.
	_, err = sqlDB.Exec(`INSERT INTO chat_descendant_questions (chat_id, tool_call_id, questions, created_at)
		VALUES ($1, 'call-1', '[{"header": "DB"}]', $2)`, root.ID, asked)
	require.NoError(t, err)
	type message struct {
		after      time.Duration
		visibility database.ChatMessageVisibility
		text       string
		compressed bool
		deleted    bool
	}
	const (
		plain  = `[{"header": "DB"}]`
		hidden = `[{"header": "Token", "secret": true}]`
	)
	answer := "Answer to ask_user_question call call-1:\nThe user gave a secret answer."
	cases := []struct {
		name       string
		questions  string
		messages   []message
		answeredBy string // empty: the question stays pending
	}{
		{name: "Visible", questions: plain, messages: []message{{after: time.Minute, visibility: database.ChatMessageVisibilityBoth}}, answeredBy: "migrated"},
		{name: "BeforeAndModelOnly", questions: plain, messages: []message{
			{after: -time.Minute, visibility: database.ChatMessageVisibilityBoth},
			{after: time.Minute, visibility: database.ChatMessageVisibilityModel},
		}},
		{name: "Deleted", questions: plain, messages: []message{{after: time.Minute, visibility: database.ChatMessageVisibilityBoth, deleted: true}}},
		{name: "Compressed", questions: hidden, messages: []message{{after: time.Minute, visibility: database.ChatMessageVisibilityModel, compressed: true}}, answeredBy: "migrated"},
		{name: "SecretVisible", questions: hidden, messages: []message{{after: time.Minute, visibility: database.ChatMessageVisibilityUser}}},
		{name: "SecretAnswer", questions: hidden, messages: []message{{after: time.Minute, visibility: database.ChatMessageVisibilityBoth, text: answer}}, answeredBy: "migrated"},
	}
	chats := make([]database.Chat, len(cases))
	for i, tc := range cases {
		chats[i] = dbgen.Chat(t, db, seed)
		_, err := sqlDB.Exec(`INSERT INTO chat_descendant_questions (chat_id, tool_call_id, questions, created_at)
			VALUES ($1, 'call-1', $2, $3)`, chats[i].ID, tc.questions, asked)
		require.NoError(t, err)
		for _, msg := range tc.messages {
			text := msg.text
			if text == "" {
				text = "Use Postgres"
			}
			content, err := json.Marshal([]map[string]string{{"type": "text", "text": text}})
			require.NoError(t, err)
			m := dbgen.ChatMessage(t, db, database.ChatMessage{
				ChatID:        chats[i].ID,
				ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
				Role:          database.ChatMessageRoleUser,
				Visibility:    msg.visibility,
				Compressed:    msg.compressed,
				Content:       pqtype.NullRawMessage{RawMessage: content, Valid: true},
			})
			_, err = sqlDB.Exec("UPDATE chat_messages SET created_at = $1, deleted = $2 WHERE id = $3", asked.Add(msg.after), msg.deleted, m.ID)
			require.NoError(t, err)
		}
	}

	require.NoError(t, migrations.UpAll(sqlDB))
	require.NoError(t, migrations.EnsureCleanAll(sqlDB))

	for i, tc := range cases {
		var (
			rootID     uuid.UUID
			answeredAt sql.NullTime
			answeredBy sql.NullString
			createdAt  time.Time
		)
		err := sqlDB.QueryRow(`SELECT root_chat_id, answered_at, answered_by, created_at
			FROM chat_descendant_questions WHERE chat_id = $1`, chats[i].ID).
			Scan(&rootID, &answeredAt, &answeredBy, &createdAt)
		require.NoError(t, err, "%s: the node keeps the row", tc.name)
		require.Equal(t, root.ID, rootID, "%s: the root of the family", tc.name)
		require.True(t, asked.Equal(createdAt), tc.name)
		require.Equal(t, tc.answeredBy != "", answeredAt.Valid, tc.name)
		require.Equal(t, tc.answeredBy, answeredBy.String, tc.name)
	}

	var rootOfRoot uuid.UUID
	require.NoError(t, sqlDB.QueryRow(`SELECT root_chat_id FROM chat_descendant_questions WHERE chat_id = $1`, root.ID).Scan(&rootOfRoot))
	require.Equal(t, root.ID, rootOfRoot)

	// An answered row must say what answered it.
	_, err = sqlDB.Exec(`UPDATE chat_descendant_questions SET answered_at = now(), answered_by = NULL WHERE chat_id = $1`, chats[1].ID)
	require.ErrorContains(t, err, "chat_descendant_questions_answered_by_check")
}
