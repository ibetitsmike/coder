package chatstate_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// TestRetireDescendantQuestion checks that every event that answers or
// retires a descendant question stores it in the transaction of the event.
func TestRetireDescendantQuestion(t *testing.T) {
	t.Parallel()

	const (
		plain  = `[{"header": "DB"}]`
		hidden = `[{"header": "Token", "secret": true}]`
	)
	answer := func(callID string) string {
		return chatstate.QuestionAnswerPrefix + callID + ":\nThe user gave a secret answer."
	}
	update := func(t *testing.T, f *testFixture, chatID uuid.UUID, fn func(*chatstate.Tx, database.Store) error) {
		t.Helper()
		ctx := testutil.Context(t, testutil.WaitShort)
		require.NoError(t, chatstate.NewChatMachine(f.DB, f.Pub, chatID).Update(ctx, fn))
	}
	send := func(t *testing.T, f *testFixture, chatID uuid.UUID, text string, behavior chatstate.BusyBehavior) {
		t.Helper()
		update(t, f, chatID, func(tx *chatstate.Tx, _ database.Store) error {
			_, err := tx.SendMessage(chatstate.SendMessageInput{
				Message:      userTextMessage(text, f.User.ID, f.Model.ID),
				BusyBehavior: behavior,
				MaxQueueSize: codersdk.DefaultChatMaxQueuedMessagesPerChat,
			})
			return err
		})
	}

	for _, tc := range []struct {
		name      string
		state     chatstate.ExecutionState
		questions string
		// steps run in order. The question must still be pending after
		// the first pending steps.
		steps      []func(t *testing.T, f *testFixture, chatID uuid.UUID)
		pending    int
		answeredBy string // empty: the question stays pending
	}{
		{
			name:      "VisibleMessage",
			state:     chatstate.StateW,
			questions: plain,
			steps: []func(*testing.T, *testFixture, uuid.UUID){
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					send(t, f, chatID, "use postgres", chatstate.BusyBehaviorQueue)
				},
			},
			answeredBy: "message",
		},
		{
			name:      "PromotedQueuedMessage",
			state:     chatstate.StateR0,
			questions: plain,
			steps: []func(*testing.T, *testFixture, uuid.UUID){
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					send(t, f, chatID, "use postgres", chatstate.BusyBehaviorQueue)
				},
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					update(t, f, chatID, func(tx *chatstate.Tx, _ database.Store) error {
						res, err := tx.FinishTurn(chatstate.FinishTurnInput{})
						if err == nil && len(res.PromotedMessages) != 1 {
							err = xerrors.Errorf("promoted %d messages, want 1", len(res.PromotedMessages))
						}
						return err
					})
				},
			},
			pending:    1,
			answeredBy: "message",
		},
		{
			name:      "SecretMessage",
			state:     chatstate.StateW,
			questions: hidden,
			steps: []func(*testing.T, *testFixture, uuid.UUID){
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					send(t, f, chatID, "the token is hunter2", chatstate.BusyBehaviorQueue)
				},
			},
			pending: 1,
		},
		{
			name:      "SecretAnswer",
			state:     chatstate.StateW,
			questions: hidden,
			steps: []func(*testing.T, *testFixture, uuid.UUID){
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					send(t, f, chatID, answer("call-other"), chatstate.BusyBehaviorQueue)
				},
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					update(t, f, chatID, func(tx *chatstate.Tx, _ database.Store) error {
						_, err := tx.FinishTurn(chatstate.FinishTurnInput{})
						return err
					})
				},
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					send(t, f, chatID, answer("call-1"), chatstate.BusyBehaviorQueue)
				},
			},
			pending:    2,
			answeredBy: "secret_answer",
		},
		{
			name:      "ClearContext",
			state:     chatstate.StateW,
			questions: hidden,
			steps: []func(*testing.T, *testFixture, uuid.UUID){
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					update(t, f, chatID, func(tx *chatstate.Tx, _ database.Store) error {
						_, err := tx.ClearContext(chatstate.ClearContextInput{Messages: clearBoundaryMessages(t)})
						return err
					})
				},
			},
			answeredBy: "context_boundary",
		},
		{
			name:      "Compaction",
			state:     chatstate.StateR0,
			questions: hidden,
			steps: []func(*testing.T, *testFixture, uuid.UUID){
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					summary := userTextMessage("summary", f.User.ID, f.Model.ID)
					summary.Visibility = database.ChatMessageVisibilityModel
					summary.Compressed = true
					update(t, f, chatID, func(tx *chatstate.Tx, _ database.Store) error {
						_, err := tx.CommitStep(chatstate.CommitStepInput{Messages: []chatstate.Message{summary}})
						return err
					})
				},
			},
			answeredBy: "context_boundary",
		},
		{
			name:      "ModelOnlyMessage",
			state:     chatstate.StateR0,
			questions: plain,
			steps: []func(*testing.T, *testFixture, uuid.UUID){
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					note := userTextMessage("note", f.User.ID, f.Model.ID)
					note.Visibility = database.ChatMessageVisibilityModel
					update(t, f, chatID, func(tx *chatstate.Tx, _ database.Store) error {
						_, err := tx.CommitStep(chatstate.CommitStepInput{Messages: []chatstate.Message{note}})
						return err
					})
				},
			},
			pending: 1,
		},
		{
			name:      "RolledBackMessage",
			state:     chatstate.StateW,
			questions: plain,
			steps: []func(*testing.T, *testFixture, uuid.UUID){
				func(t *testing.T, f *testFixture, chatID uuid.UUID) {
					ctx := testutil.Context(t, testutil.WaitShort)
					rollback := xerrors.New("roll back")
					err := chatstate.NewChatMachine(f.DB, f.Pub, chatID).Update(ctx, func(tx *chatstate.Tx, _ database.Store) error {
						if _, err := tx.SendMessage(chatstate.SendMessageInput{
							Message:      userTextMessage("use postgres", f.User.ID, f.Model.ID),
							BusyBehavior: chatstate.BusyBehaviorQueue,
							MaxQueueSize: codersdk.DefaultChatMaxQueuedMessagesPerChat,
						}); err != nil {
							return err
						}
						return rollback
					})
					require.ErrorIs(t, err, rollback)
				},
			},
			pending: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, sqlDB := newDescendantQuestionFixture(t)
			ctx := testutil.Context(t, testutil.WaitShort)
			fdb, err := forkdb.From(f.DB)
			require.NoError(t, err)
			chatID := seedState(t, f, tc.state).chatID
			_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
				ChatID:     chatID,
				ToolCallID: "call-1",
				Questions:  json.RawMessage(tc.questions),
			})
			require.NoError(t, err)

			for i, step := range tc.steps {
				step(t, f, chatID)
				answeredBy, answered := descendantQuestionAnswer(ctx, t, sqlDB, chatID)
				if i < tc.pending {
					require.False(t, answered, "pending after step %d", i)
					continue
				}
				require.Equal(t, tc.answeredBy != "", answered, "answered after step %d", i)
				require.Equal(t, tc.answeredBy, answeredBy)
			}

			if tc.answeredBy == "" {
				return
			}
			// A new question of the chat is pending again.
			_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
				ChatID:     chatID,
				ToolCallID: "call-2",
				Questions:  json.RawMessage(plain),
			})
			require.NoError(t, err)
			_, answered := descendantQuestionAnswer(ctx, t, sqlDB, chatID)
			require.False(t, answered)
		})
	}
}

// ForkUnwrap and ForkWrap let forkdb.From reach the fork queries of the
// wrapped stores, which retire a question when a user message is inserted.
func (s *queueDeleteCountStore) ForkUnwrap() database.Store { return s.Store }

func (*queueDeleteCountStore) ForkWrap(s forkdb.Store) forkdb.Store { return s }

func (s *modelConfigErrorStore) ForkUnwrap() database.Store { return s.Store }

func (*modelConfigErrorStore) ForkWrap(s forkdb.Store) forkdb.Store { return s }

// newDescendantQuestionFixture is newTestFixture with the raw connection,
// which reads the stored answer.
func newDescendantQuestionFixture(t *testing.T) (*testFixture, *sql.DB) {
	t.Helper()
	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	user := dbgen.User(t, db, database.User{})
	org := dbgen.Organization(t, db, database.Organization{})
	dbgen.OrganizationMember(t, db, database.OrganizationMember{
		UserID:         user.ID,
		OrganizationID: org.ID,
	})
	dbgen.ChatProvider(t, db, database.ChatProvider{
		Provider:    "openai",
		DisplayName: "openai",
		BaseUrl:     "http://example.invalid",
	})
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{
		OrganizationID: org.ID,
		IsDefault:      true,
	})
	return &testFixture{
		DB:    db,
		Pub:   newRecordingPubsub(),
		User:  user,
		Org:   org,
		Model: model,
	}, sqlDB
}

// descendantQuestionAnswer returns what answered the question of chatID,
// and whether it is answered.
func descendantQuestionAnswer(ctx context.Context, t *testing.T, sqlDB *sql.DB, chatID uuid.UUID) (string, bool) {
	t.Helper()
	var (
		answeredAt sql.NullTime
		answeredBy sql.NullString
	)
	err := sqlDB.QueryRowContext(ctx, "SELECT answered_at, answered_by FROM chatd.chat_descendant_questions WHERE chat_id = $1", chatID).
		Scan(&answeredAt, &answeredBy)
	require.NoError(t, err)
	require.Equal(t, answeredAt.Valid, answeredBy.Valid)
	return answeredBy.String, answeredAt.Valid
}
