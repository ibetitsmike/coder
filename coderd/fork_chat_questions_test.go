package coderd_test

// This file is fork code: it covers the descendant questions routes of the
// fork-nested-subagents experiment.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/agenthooks"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

func TestForkChatDescendantQuestions(t *testing.T) {
	t.Parallel()

	newAPI := func(t *testing.T, enabled bool) (*codersdk.Client, database.Store, codersdk.CreateFirstUserResponse) {
		db, ps := dbtestutil.NewDB(t)
		values := coderdtest.DeploymentValues(t)
		if enabled {
			values.Experiments = serpent.StringArray{string(codersdk.ExperimentForkNestedSubagents)}
		}
		client := coderdtest.New(t, &coderdtest.Options{DeploymentValues: values, Database: db, Pubsub: ps})
		return client, db, coderdtest.CreateFirstUser(t, client)
	}
	newChat := func(t *testing.T, db database.Store, user codersdk.CreateFirstUserResponse, parent *database.Chat) database.Chat {
		seed := database.Chat{
			OrganizationID: user.OrganizationID,
			OwnerID:        user.UserID,
			LastModelConfigID: dbgen.ChatModelConfig(t, db, database.ChatModelConfig{
				OrganizationID: user.OrganizationID,
			}).ID,
		}
		if parent != nil {
			seed.ParentChatID = uuid.NullUUID{UUID: parent.ID, Valid: true}
			seed.RootChatID = parent.RootChatID
			if !parent.RootChatID.Valid {
				seed.RootChatID = seed.ParentChatID
			}
		}
		return dbgen.Chat(t, db, seed)
	}
	path := func(chatID uuid.UUID) string {
		return "/api/v2/chats/fork/" + chatID.String() + "/questions"
	}

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, db, user := newAPI(t, false)
		chat := newChat(t, db, user, nil)

		res, err := client.Request(ctx, http.MethodGet, path(chat.ID), nil)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	t.Run("Family", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, db, user := newAPI(t, true)
		fdb, err := forkdb.From(db)
		require.NoError(t, err)
		root := newChat(t, db, user, nil)
		child := newChat(t, db, user, &root)
		grandchild := newChat(t, db, user, &child)
		ask := func(c database.Chat, callID, questions string) time.Time {
			row, err := fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
				ChatID:     c.ID,
				ToolCallID: callID,
				Questions:  json.RawMessage(questions),
			})
			require.NoError(t, err)
			return row.CreatedAt
		}
		grandchildAsked := ask(grandchild, "call-grandchild",
			`[{"header": "DB", "question": "Which database?", "options": [{"label": "Postgres"}, {"label": "SQLite"}]}]`)
		childAsked := ask(child, "call-child",
			`[{"header": "API", "question": "REST or gRPC?", "options": [{"label": "REST"}]}]`)
		// A row that does not decode is skipped instead of failing the family.
		_ = ask(newChat(t, db, user, &root), "call-malformed", `{"header": "not a list"}`)
		want := []codersdk.ChatDescendantQuestion{{
			ChatID:       grandchild.ID,
			ParentChatID: child.ID,
			ChatTitle:    grandchild.Title,
			ToolCallID:   "call-grandchild",
			Questions: []codersdk.ChatQuestion{{
				Header:   "DB",
				Question: "Which database?",
				Options:  []codersdk.ChatQuestionOption{{Label: "Postgres"}, {Label: "SQLite"}},
			}},
		}, {
			ChatID:       child.ID,
			ParentChatID: root.ID,
			ChatTitle:    child.Title,
			ToolCallID:   "call-child",
			Questions: []codersdk.ChatQuestion{{
				Header:   "API",
				Question: "REST or gRPC?",
				Options:  []codersdk.ChatQuestionOption{{Label: "REST"}},
			}},
		}}

		// A child's ID lists the same family as the root's.
		for _, chatID := range []uuid.UUID{root.ID, child.ID} {
			got, err := client.ChatDescendantQuestions(ctx, chatID)
			require.NoError(t, err)
			require.Len(t, got, 2)
			require.True(t, grandchildAsked.Equal(got[0].CreatedAt))
			require.True(t, childAsked.Equal(got[1].CreatedAt))
			got[0].CreatedAt, got[1].CreatedAt = time.Time{}, time.Time{}
			require.Equal(t, want, got)
		}

		// A family without questions answers an empty array, never null.
		res, err := client.Request(ctx, http.MethodGet, path(newChat(t, db, user, nil).ID), nil)
		require.NoError(t, err)
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.JSONEq(t, `[]`, string(body))

		other, _ := coderdtest.CreateAnotherUser(t, client, user.OrganizationID)
		res, err = other.Request(ctx, http.MethodGet, path(root.ID), nil)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})
}

func TestForkChatSecretAnswer(t *testing.T) {
	t.Parallel()

	const secret = "hunter2-secret-value"
	ctx := testutil.Context(t, testutil.WaitLong)
	client, db := newChatClientWithDatabase(t, func(opts *coderdtest.Options) {
		opts.DeploymentValues.Experiments = append(
			opts.DeploymentValues.Experiments,
			string(codersdk.ExperimentForkNestedSubagents),
		)
	})
	user := coderdtest.CreateFirstUser(t, client.Client)
	modelConfig := createChatModel(t, client)
	root, child, grandchild := forkChatFamily(t, db, user.OrganizationID, user.UserID, modelConfig.ID)
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	ask := func(c database.Chat, callID, questions string) {
		_, err := fdb.UpsertChatDescendantQuestion(sysCtx, forkdb.UpsertChatDescendantQuestionParams{
			ChatID: c.ID, ToolCallID: callID, Questions: json.RawMessage(questions),
		})
		require.NoError(t, err)
	}
	earlier := dbgen.ChatMessage(t, db, database.ChatMessage{
		ChatID:        child.ID,
		ModelConfigID: uuid.NullUUID{UUID: modelConfig.ID, Valid: true},
		Role:          database.ChatMessageRoleUser,
		Visibility:    database.ChatMessageVisibilityBoth,
	})
	ask(child, "call-secret", `[{"header": "Token", "question": "Paste the API token.", "secret": true}]`)
	ask(grandchild, "call-normal", `[{"header": "DB", "question": "Which database?", "options": [{"label": "Postgres"}]}]`)
	answerText := func(callID, answer string) codersdk.CreateChatMessageRequest {
		return codersdk.CreateChatMessageRequest{Content: []codersdk.ChatInputPart{{
			Type: codersdk.ChatInputPartTypeText,
			Text: "Answer to ask_user_question call " + callID + ":\n" + answer,
		}}}
	}
	requireStatus := func(t *testing.T, want int, err error) {
		t.Helper()
		var sdkErr *codersdk.Error
		require.ErrorAs(t, err, &sdkErr)
		require.Equal(t, want, sdkErr.StatusCode())
	}
	requireSecretRefusal := func(t *testing.T, err error) {
		t.Helper()
		var sdkErr *codersdk.Error
		require.ErrorAs(t, err, &sdkErr)
		require.Equal(t, http.StatusConflict, sdkErr.StatusCode())
		require.Contains(t, sdkErr.Message, "takes a secret answer")
	}
	secretText := []codersdk.ChatInputPart{{Type: codersdk.ChatInputPartTypeText, Text: secret}}

	listed, err := client.ChatDescendantQuestions(ctx, root.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	for _, q := range listed {
		for _, item := range q.Questions {
			require.NotNil(t, item.Options, "options serialize as an array, not null")
		}
	}

	// The normal answer path cannot carry a secret answer into the chat,
	// with or without the answer prefix.
	_, err = client.CreateChatMessage(ctx, child.ID, answerText("call-secret", secret))
	requireStatus(t, http.StatusConflict, err)
	_, err = client.CreateChatMessage(ctx, child.ID, codersdk.CreateChatMessageRequest{Content: secretText})
	requireSecretRefusal(t, err)
	// Neither can an edit of an earlier message.
	_, err = client.EditChatMessage(ctx, child.ID, earlier.ID, codersdk.EditChatMessageRequest{Content: secretText})
	requireSecretRefusal(t, err)

	err = client.AnswerChatSecretQuestion(ctx, child.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-other", Value: secret})
	requireStatus(t, http.StatusConflict, err)
	err = client.AnswerChatSecretQuestion(ctx, child.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-secret", Value: "  "})
	requireStatus(t, http.StatusBadRequest, err)
	err = client.AnswerChatSecretQuestion(ctx, child.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-secret", Value: "short"})
	requireStatus(t, http.StatusBadRequest, err)
	err = client.AnswerChatSecretQuestion(ctx, child.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-secret", Value: "before\x00after"})
	requireStatus(t, http.StatusBadRequest, err)
	err = client.AnswerChatSecretQuestion(ctx, grandchild.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-normal", Value: secret})
	requireStatus(t, http.StatusBadRequest, err)
	other, _ := coderdtest.CreateAnotherUser(t, client.Client, user.OrganizationID)
	err = codersdk.NewExperimentalClient(other).AnswerChatSecretQuestion(ctx, child.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-secret", Value: secret})
	requireStatus(t, http.StatusNotFound, err)

	require.NoError(t, client.AnswerChatSecretQuestion(ctx, child.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-secret", Value: secret}))
	answers, err := fdb.GetChatSecretAnswersByChatID(sysCtx, child.ID)
	require.NoError(t, err)
	require.Len(t, answers, 1)
	require.Equal(t, "call-secret", answers[0].ToolCallID)
	require.Equal(t, secret, answers[0].Value)
	messages, err := db.GetChatMessagesByChatID(sysCtx, database.GetChatMessagesByChatIDParams{ChatID: child.ID})
	require.NoError(t, err)
	var answered bool
	for _, msg := range messages {
		require.NotContains(t, string(msg.Content.RawMessage), secret)
		if msg.Role == database.ChatMessageRoleUser {
			answered = answered || strings.Contains(string(msg.Content.RawMessage), "The user gave a secret answer.")
		}
	}
	require.True(t, answered, "the chat gets a user message without the value")
	err = client.AnswerChatSecretQuestion(ctx, child.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-secret", Value: secret})
	requireStatus(t, http.StatusConflict, err)

	// A busy chat queues the message that reports the answer. Once the
	// owner deletes that message, the user can answer again.
	_, busy, _ := forkChatFamily(t, db, user.OrganizationID, user.UserID, modelConfig.ID)
	ask(busy, "call-busy", `[{"header": "Token", "question": "Paste the API token.", "secret": true}]`)
	_, err = db.UpdateChatStatus(sysCtx, database.UpdateChatStatusParams{
		ID:          busy.ID,
		Status:      database.ChatStatusRunning,
		WorkerID:    uuid.NullUUID{UUID: uuid.New(), Valid: true},
		StartedAt:   sql.NullTime{Time: time.Now(), Valid: true},
		HeartbeatAt: sql.NullTime{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.NoError(t, client.AnswerChatSecretQuestion(ctx, busy.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-busy", Value: "first-value"}))
	err = client.AnswerChatSecretQuestion(ctx, busy.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-busy", Value: "second-value"})
	requireStatus(t, http.StatusConflict, err)
	queued, err := db.GetChatQueuedMessages(sysCtx, busy.ID)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	// Nor an edit of a queued message.
	res, err := client.Request(ctx, http.MethodPatch, fmt.Sprintf("/api/v2/chats/%s/queue/%d", busy.ID, queued[0].ID),
		codersdk.EditChatQueuedMessageRequest{Content: secretText})
	require.NoError(t, err)
	requireSecretRefusal(t, codersdk.ReadBodyAsError(res))
	_ = res.Body.Close()
	res, err = client.Request(ctx, http.MethodDelete, fmt.Sprintf("/api/v2/chats/%s/queue/%d", busy.ID, queued[0].ID), nil)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.NoError(t, client.AnswerChatSecretQuestion(ctx, busy.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-busy", Value: "second-value"}))
	answers, err = fdb.GetChatSecretAnswersByChatID(sysCtx, busy.ID)
	require.NoError(t, err)
	require.Len(t, answers, 1)
	require.Equal(t, "second-value", answers[0].Value)
	queued, err = db.GetChatQueuedMessages(sysCtx, busy.ID)
	require.NoError(t, err)
	require.Len(t, queued, 1, "the answer is reported again")

	// The ID travels in the body, so an ID that a path would escape matches.
	for _, callID := range []string{"call/a%41", "call a%41"} {
		_, escaped, _ := forkChatFamily(t, db, user.OrganizationID, user.UserID, modelConfig.ID)
		ask(escaped, callID, `[{"header": "Token", "question": "Paste the API token.", "secret": true}]`)
		require.NoError(t, client.AnswerChatSecretQuestion(ctx, escaped.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: callID, Value: secret}), callID)
	}

	// The user can decline a secret question, such as for a value too
	// short to hide, which answers it without a value.
	_, declined, _ := forkChatFamily(t, db, user.OrganizationID, user.UserID, modelConfig.ID)
	ask(declined, "call-decline", `[{"header": "PIN", "question": "Enter the PIN.", "secret": true}]`)
	err = client.AnswerChatSecretQuestion(ctx, declined.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-decline", Value: secret, Declined: true})
	requireStatus(t, http.StatusBadRequest, err)
	err = client.AnswerChatSecretQuestion(ctx, declined.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-other", Declined: true})
	requireStatus(t, http.StatusConflict, err)
	err = client.AnswerChatSecretQuestion(ctx, grandchild.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-normal", Declined: true})
	requireStatus(t, http.StatusBadRequest, err)
	// A stored answer whose report failed does not outlive the decline.
	require.NoError(t, fdb.InsertChatSecretAnswer(sysCtx, forkdb.InsertChatSecretAnswerParams{ChatID: declined.ID, ToolCallID: "call-decline", Value: secret}))
	require.NoError(t, client.AnswerChatSecretQuestion(ctx, declined.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-decline", Declined: true}))
	_, err = fdb.GetPendingChatDescendantQuestion(sysCtx, declined.ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "declining answers the secret question")
	answers, err = fdb.GetChatSecretAnswersByChatID(sysCtx, declined.ID)
	require.NoError(t, err)
	require.Empty(t, answers)
	_, err = client.CreateChatMessage(ctx, declined.ID, codersdk.CreateChatMessageRequest{Content: []codersdk.ChatInputPart{{
		Type: codersdk.ChatInputPartTypeText, Text: "The PIN is 1234.",
	}}})
	require.NoError(t, err, "a declined secret question no longer refuses messages")

	// A normal question still takes a normal answer.
	_, err = client.CreateChatMessage(ctx, grandchild.ID, answerText("call-normal", "Postgres"))
	require.NoError(t, err)
}

// A user_prompt_submit hook that rewrites prompts does not rewrite the
// message of the secret answer route, which must retire the question.
func TestForkChatSecretAnswerPromptOverride(t *testing.T) {
	t.Parallel()

	const hookSecret = "test-hook-secret-32-bytes-minimum!!"
	ctx := testutil.Context(t, testutil.WaitLong)
	consumer := newHookConsumer(t, hookSecret, agenthooks.Hooks{
		UserPromptSubmit: func(context.Context, agenthooks.Meta, agenthooks.UserPromptSubmitData) (agenthooks.Response, error) {
			return agenthooks.Response{Permission: &agenthooks.Permission{
				Decision:      agenthooks.PermissionAllow,
				InputOverride: json.RawMessage(`{"prompt":"redacted"}`),
			}}, nil
		},
	})
	t.Cleanup(consumer.Close)
	client, db := newChatClientWithDatabase(t, func(opts *coderdtest.Options) {
		opts.ChatWorkerDisabled = true
		opts.DeploymentValues.Experiments = append(
			opts.DeploymentValues.Experiments,
			string(codersdk.ExperimentForkNestedSubagents),
		)
		require.NoError(t, opts.DeploymentValues.AI.Chat.HookURL.Set(consumer.URL))
		opts.DeploymentValues.AI.Chat.HookSecret = serpent.String(hookSecret)
		opts.DeploymentValues.AI.Chat.HookTimeout = serpent.Duration(time.Second)
		opts.DeploymentValues.AI.Chat.HookEnabled = serpent.Bool(true)
	})
	user := coderdtest.CreateFirstUser(t, client.Client)
	modelConfig := createChatModel(t, client)
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)

	for _, req := range []codersdk.ChatSecretAnswerRequest{
		{ToolCallID: "call-secret", Value: "hunter2-secret-value"},
		{ToolCallID: "call-secret", Declined: true},
	} {
		_, child, _ := forkChatFamily(t, db, user.OrganizationID, user.UserID, modelConfig.ID)
		_, err := fdb.UpsertChatDescendantQuestion(sysCtx, forkdb.UpsertChatDescendantQuestionParams{
			ChatID:     child.ID,
			ToolCallID: "call-secret",
			Questions:  json.RawMessage(`[{"header": "Token", "question": "Paste the API token.", "secret": true}]`),
		})
		require.NoError(t, err)
		require.NoError(t, client.AnswerChatSecretQuestion(ctx, child.ID, req))
		_, err = fdb.GetPendingChatDescendantQuestion(sysCtx, child.ID)
		require.ErrorIs(t, err, sql.ErrNoRows, "declined=%v", req.Declined)
	}
}

// With the experiment off, the secret answer route is gone, so a chat
// message still resumes a chat whose question is secret.
func TestForkChatSecretQuestionExperimentOff(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db := newChatClientWithDatabase(t)
	user := coderdtest.CreateFirstUser(t, client.Client)
	modelConfig := createChatModel(t, client)
	_, child, _ := forkChatFamily(t, db, user.OrganizationID, user.UserID, modelConfig.ID)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	_, err = fdb.UpsertChatDescendantQuestion(dbauthz.AsSystemRestricted(ctx), forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     child.ID,
		ToolCallID: "call-secret",
		Questions:  json.RawMessage(`[{"header": "Token", "question": "Paste the API token.", "secret": true}]`),
	})
	require.NoError(t, err)

	_, err = client.CreateChatMessage(ctx, child.ID, codersdk.CreateChatMessageRequest{Content: []codersdk.ChatInputPart{{
		Type: codersdk.ChatInputPartTypeText,
		Text: "Skip the token.",
	}}})
	require.NoError(t, err)
}

// A message that the user queued before the child asked a secret question
// does not answer the question when the end of the turn promotes it.
func TestForkChatSecretQuestionQueuedMessage(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db, api := newChatClientWithAPIAndDatabase(t, withChatWorkerDisabled, func(opts *coderdtest.Options) {
		opts.DeploymentValues.Experiments = append(
			opts.DeploymentValues.Experiments,
			string(codersdk.ExperimentForkNestedSubagents),
		)
	})
	user := coderdtest.CreateFirstUser(t, client.Client)
	modelConfig := createChatModel(t, client)
	root, child, _ := forkChatFamily(t, db, user.OrganizationID, user.UserID, modelConfig.ID)
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	_, err := db.UpdateChatStatus(sysCtx, database.UpdateChatStatusParams{
		ID:          child.ID,
		Status:      database.ChatStatusRunning,
		WorkerID:    uuid.NullUUID{UUID: uuid.New(), Valid: true},
		StartedAt:   sql.NullTime{Time: time.Now(), Valid: true},
		HeartbeatAt: sql.NullTime{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	_, err = client.CreateChatMessage(ctx, child.ID, codersdk.CreateChatMessageRequest{Content: []codersdk.ChatInputPart{{
		Type: codersdk.ChatInputPartTypeText,
		Text: "Also check the staging database.",
	}}})
	require.NoError(t, err)
	queued, err := db.GetChatQueuedMessages(sysCtx, child.ID)
	require.NoError(t, err)
	require.Len(t, queued, 1)

	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	_, err = fdb.UpsertChatDescendantQuestion(sysCtx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     child.ID,
		ToolCallID: "call-secret",
		Questions:  json.RawMessage(`[{"header": "Token", "question": "Paste the API token.", "secret": true}]`),
	})
	require.NoError(t, err)
	// FinishTurn promotes the queue head into the chat's messages.
	driveChatToWaiting(ctx, t, api, child.ID)
	queued, err = db.GetChatQueuedMessages(sysCtx, child.ID)
	require.NoError(t, err)
	require.Empty(t, queued, "the end of the turn delivers the queued message")

	listed, err := client.ChatDescendantQuestions(ctx, root.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1, "the secret question stays pending")
	require.Equal(t, "call-secret", listed[0].ToolCallID)
}

// An answer past its TTL is replaced, even while the message that reports
// it is still queued: reads skip it before dbpurge deletes it. The queued
// message then reports the new answer.
func TestForkChatSecretAnswerExpired(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, ps, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	client, _ := newChatClientWithDatabase(t, func(opts *coderdtest.Options) {
		opts.Database = db
		opts.Pubsub = ps
		opts.DeploymentValues.Experiments = append(
			opts.DeploymentValues.Experiments,
			string(codersdk.ExperimentForkNestedSubagents),
		)
	})
	user := coderdtest.CreateFirstUser(t, client.Client)
	modelConfig := createChatModel(t, client)
	_, busy, _ := forkChatFamily(t, db, user.OrganizationID, user.UserID, modelConfig.ID)
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	_, err = fdb.UpsertChatDescendantQuestion(sysCtx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     busy.ID,
		ToolCallID: "call-busy",
		Questions:  json.RawMessage(`[{"header": "Token", "question": "Paste the API token.", "secret": true}]`),
	})
	require.NoError(t, err)
	_, err = db.UpdateChatStatus(sysCtx, database.UpdateChatStatusParams{
		ID:          busy.ID,
		Status:      database.ChatStatusRunning,
		WorkerID:    uuid.NullUUID{UUID: uuid.New(), Valid: true},
		StartedAt:   sql.NullTime{Time: time.Now(), Valid: true},
		HeartbeatAt: sql.NullTime{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.NoError(t, client.AnswerChatSecretQuestion(ctx, busy.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-busy", Value: "first-value"}))
	_, err = sqlDB.ExecContext(ctx,
		"UPDATE chat_secret_answers SET created_at = now() - interval '25 hours' WHERE chat_id = $1",
		busy.ID,
	)
	require.NoError(t, err)

	require.NoError(t, client.AnswerChatSecretQuestion(ctx, busy.ID, codersdk.ChatSecretAnswerRequest{ToolCallID: "call-busy", Value: "second-value"}))
	answers, err := fdb.GetChatSecretAnswersByChatID(sysCtx, busy.ID)
	require.NoError(t, err)
	require.Len(t, answers, 1)
	require.Equal(t, "second-value", answers[0].Value)
	queued, err := db.GetChatQueuedMessages(sysCtx, busy.ID)
	require.NoError(t, err)
	require.Len(t, queued, 1, "the queued message reports the new answer")
}
