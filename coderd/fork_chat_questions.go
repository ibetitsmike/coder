package coderd

// This file is fork code: it lists and answers the descendant questions of
// the fork-nested-subagents experiment. registerForkChatRoutes mounts the
// handlers.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/db2sdk"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpapi/httperror"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
)

const (
	// questionAnswerPrefix starts the user message that answers an
	// ask_user_question call; the tool tells the model to expect it.
	questionAnswerPrefix = "Answer to ask_user_question call "
	// minSecretAnswerLength keeps the scrub of tool output, which replaces
	// every occurrence of the value, from hitting ordinary text.
	minSecretAnswerLength = 8
	maxSecretAnswerLength = 16 << 10
)

// @Summary List pending descendant questions
// @ID list-pending-descendant-questions
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Success 200 {array} codersdk.ChatDescendantQuestion
// @Router /api/v2/chats/fork/{chat}/questions [get]
// @x-apidocgen {"skip": true}
// @Description Returns the unanswered ask_user_question calls of the
// @Description unarchived descendants in the family of the chat, oldest first.
// @Description A child chat lists the same family as its root.
func (api *API) getChatDescendantQuestions(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)
	rootID := chat.ID
	if chat.RootChatID.Valid {
		rootID = chat.RootChatID.UUID
	}

	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	// The query runs as the request actor, so dbauthz requires read access
	// to the family's root chat.
	rows, err := fdb.ListPendingChatDescendantQuestionsByRoot(ctx, rootID)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to list descendant questions.",
			Detail:  err.Error(),
		})
		return
	}

	questions := make([]codersdk.ChatDescendantQuestion, 0, len(rows))
	for _, row := range rows {
		var items []codersdk.ChatQuestion
		if err := json.Unmarshal(row.Questions, &items); err != nil {
			// One bad row must not hide the other questions of the family.
			api.Logger.Warn(ctx, "decode descendant question",
				slog.F("chat_id", row.ChatID), slog.Error(err))
			continue
		}
		// Secret questions have no options; clients iterate them.
		for i := range items {
			if items[i].Options == nil {
				items[i].Options = []codersdk.ChatQuestionOption{}
			}
		}
		questions = append(questions, codersdk.ChatDescendantQuestion{
			ChatID:       row.ChatID,
			ParentChatID: row.ParentChatID.UUID,
			ChatTitle:    row.ChatTitle,
			ToolCallID:   row.ToolCallID,
			Questions:    items,
			CreatedAt:    row.CreatedAt,
		})
	}
	httpapi.Write(ctx, rw, http.StatusOK, questions)
}

// @Summary Answer secret descendant question
// @ID answer-secret-descendant-question
// @Security CoderSessionToken
// @Accept json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param request body codersdk.ChatSecretAnswerRequest true "Secret answer"
// @Success 204
// @Router /api/v2/chats/fork/{chat}/questions/secret-answer [post]
// @x-apidocgen {"skip": true}
// @Description Stores the answer to the pending secret question of the chat
// @Description outside its messages, and sends the chat a user message that
// @Description says only that the user answered. A declined answer stores
// @Description nothing and says that the user declined.
func (api *API) postChatSecretAnswer(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKey := httpmw.APIKey(r)
	chat := httpmw.ChatParam(r)

	if maybeWriteChatError(ctx, rw, api.checkSendChatMessage(ctx, apiKey.UserID, chat)) {
		return
	}
	var req codersdk.ChatSecretAnswerRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	toolCallID := req.ToolCallID
	if req.Declined {
		api.declineSecretQuestion(ctx, rw, apiKey.UserID, chat, req)
		return
	}
	if strings.TrimSpace(req.Value) == "" {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "The answer must not be empty."})
		return
	}
	if len(req.Value) < minSecretAnswerLength {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: fmt.Sprintf("Secret answers must be at least %d bytes so they can be hidden from tool output. "+
				"Decline the question instead, and the subagent can ask again without the secret option.", minSecretAnswerLength),
		})
		return
	}
	if strings.Contains(req.Value, "\x00") {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "The answer must not contain null bytes."})
		return
	}
	if len(req.Value) > maxSecretAnswerLength {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: fmt.Sprintf("The answer must be at most %d bytes.", maxSecretAnswerLength),
		})
		return
	}

	// Errors stay out of the responses below: a database error can quote
	// the value.
	reportQueued, err := storeSecretAnswer(ctx, api.Database, forkdb.InsertChatSecretAnswerParams{
		ChatID:     chat.ID,
		ToolCallID: toolCallID,
		Value:      req.Value,
	})
	switch {
	case errors.Is(err, errSecretQuestionNotPending):
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "The chat has no pending question with this tool call ID.",
		})
		return
	case errors.Is(err, errSecretQuestionNotSecret):
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "The question takes a normal answer. Send it as a chat message.",
		})
		return
	case errors.Is(err, errSecretAnswerReported):
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{Message: "The question already has an answer."})
		return
	case err != nil:
		api.Logger.Error(ctx, "store secret answer", slog.F("chat_id", chat.ID))
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{Message: "Failed to store the answer."})
		return
	}
	if reportQueued {
		rw.WriteHeader(http.StatusNoContent)
		return
	}

	_, err = api.sendChatMessage(chatd.WithSecretAnswerReport(ctx, toolCallID), apiKey.UserID, chat, codersdk.CreateChatMessageRequest{
		Content: []codersdk.ChatInputPart{{
			Type: codersdk.ChatInputPartTypeText,
			Text: questionAnswerPrefix + toolCallID + ":\nThe user gave a secret answer. Write {{secret:" + toolCallID + "}} where the value belongs in execute, write_file or edit_files input.",
		}},
	})
	if errors.Is(err, chatd.ErrSecretAnswerNotPending) {
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "The chat has no pending question with this tool call ID.",
		})
		return
	}
	if err != nil {
		// The answer stays stored. The question stays pending without the
		// message, and the next answer replaces it as an unreported one.
		writeChatError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// declineSecretQuestion sends the chat a user message that says the user
// declined its pending secret question, which retires the question without
// a value.
func (api *API) declineSecretQuestion(ctx context.Context, rw http.ResponseWriter, userID uuid.UUID, chat database.Chat, req codersdk.ChatSecretAnswerRequest) {
	if req.Value != "" {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "A declined answer must not have a value."})
		return
	}
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	question, err := fdb.GetPendingChatDescendantQuestion(ctx, chat.ID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && question.ToolCallID != req.ToolCallID) {
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "The chat has no pending question with this tool call ID.",
		})
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	if !question.Secret() {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "The question takes a normal answer. Send it as a chat message.",
		})
		return
	}
	_, err = api.sendChatMessage(chatd.WithSecretAnswerDecline(ctx, req.ToolCallID), userID, chat, codersdk.CreateChatMessageRequest{
		Content: []codersdk.ChatInputPart{{
			Type: codersdk.ChatInputPartTypeText,
			Text: questionAnswerPrefix + req.ToolCallID + ":\nThe user declined to give a secret answer. Ask again without the secret option if the value is not sensitive, or continue without it.",
		}},
	})
	if errors.Is(err, chatd.ErrSecretAnswerNotPending) {
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "The chat has no pending question with this tool call ID.",
		})
		return
	}
	if errors.Is(err, chatd.ErrSecretAnswerQueued) {
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{Message: "The question already has an answer."})
		return
	}
	if err != nil {
		writeChatError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// errSecretQuestionNotPending means that the question of a secret answer
// is not the pending question of the chat.
var errSecretQuestionNotPending = xerrors.New("the question is not pending")

// errSecretQuestionNotSecret means that the pending question of a secret
// answer takes a normal answer.
var errSecretQuestionNotSecret = xerrors.New("the question is not secret")

// errSecretAnswerReported means that a queued message reports the stored
// answer.
var errSecretAnswerReported = xerrors.New("the secret answer is reported")

// storeSecretAnswer stores the answer to the pending secret question of
// arg.ToolCallID. It replaces a stored answer that no message reports: the
// owner can delete the queued message that reports an answer, and a failed
// send leaves none. The chat row lock serializes it with the queue's
// promotion and with the recording of a new question, so the question
// cannot be answered or replaced between the check and the insert, which
// would leave an answer that no question takes. reportQueued is true when a
// queued message already reports the replaced answer, so the caller must
// not queue another.
func storeSecretAnswer(ctx context.Context, db database.Store, arg forkdb.InsertChatSecretAnswerParams) (reportQueued bool, err error) {
	err = forkdb.InTx(db, func(tx database.Store, ftx forkdb.Store) error {
		if _, err := tx.GetChatByIDForUpdate(ctx, arg.ChatID); err != nil {
			return err
		}
		// A delivered message answers the question.
		question, err := ftx.GetPendingChatDescendantQuestion(ctx, arg.ChatID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && question.ToolCallID != arg.ToolCallID) {
			return errSecretQuestionNotPending
		}
		if err != nil {
			return err
		}
		if !question.Secret() {
			return errSecretQuestionNotSecret
		}
		queued, err := secretAnswerQueued(ctx, tx, arg.ChatID, arg.ToolCallID)
		if err != nil {
			return err
		}
		if queued {
			// Reads skip an answer past its TTL before dbpurge deletes
			// it, so an expired answer is replaced like an unreported one,
			// and the queued message reports the new answer.
			live, err := secretAnswerLive(ctx, ftx, arg.ChatID, arg.ToolCallID)
			if err != nil {
				return err
			}
			if live {
				return errSecretAnswerReported
			}
		}
		if err := ftx.DeleteChatSecretAnswer(ctx, forkdb.DeleteChatSecretAnswerParams{ChatID: arg.ChatID, ToolCallID: arg.ToolCallID}); err != nil {
			return err
		}
		reportQueued = queued
		return ftx.InsertChatSecretAnswer(ctx, arg)
	}, nil)
	return reportQueued, err
}

// secretAnswerQueued reports whether a queued message of the chat reports
// the answer to the question of toolCallID.
func secretAnswerQueued(ctx context.Context, db database.Store, chatID uuid.UUID, toolCallID string) (bool, error) {
	messages, err := db.GetChatQueuedMessages(ctx, chatID)
	if err != nil {
		return false, err
	}
	prefix := questionAnswerPrefix + toolCallID + ":"
	for _, message := range messages {
		for _, part := range db2sdk.ChatQueuedMessage(message).Content {
			if part.Type == codersdk.ChatMessagePartTypeText && strings.HasPrefix(part.Text, prefix) {
				return true, nil
			}
		}
	}
	return false, nil
}

// secretAnswerLive reports whether the stored answer to the question of
// toolCallID is within its TTL.
func secretAnswerLive(ctx context.Context, fdb forkdb.Store, chatID uuid.UUID, toolCallID string) (bool, error) {
	answers, err := fdb.GetChatSecretAnswersByChatID(ctx, chatID)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(answers, func(answer forkdb.ChatSecretAnswer) bool {
		return answer.ToolCallID == toolCallID
	}), nil
}

// rejectSecretQuestionAnswer refuses every message, new or edited, to a chat
// whose pending question is secret. Any user message answers the pending question, and
// only postChatSecretAnswer keeps the value out of the chat's messages.
// Errors implement httperror.Responder.
func (api *API) rejectSecretQuestionAnswer(ctx context.Context, chat database.Chat) error {
	// With the experiment off, the secret answer route is gone, so a chat
	// message is the only way to resume the chat.
	if !chat.ParentChatID.Valid || !api.Experiments.Enabled(codersdk.ExperimentForkNestedSubagents) {
		return nil
	}
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		return httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to read the chat's question.",
			Detail:  err.Error(),
		})
	}
	question, err := fdb.GetPendingChatDescendantQuestion(ctx, chat.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to read the chat's question.",
			Detail:  err.Error(),
		})
	}
	if !question.Secret() {
		return nil
	}
	return secretQuestionPendingError(nil)
}

// secretQuestionPendingError is the response to a message for a chat whose
// pending question is secret. SendMessage and EditMessage return
// chatd.ErrSecretQuestionPending when the question is recorded after
// rejectSecretQuestionAnswer runs.
func secretQuestionPendingError(err error) error {
	return httperror.WrapResponseError(http.StatusConflict, codersdk.Response{
		Message: "The chat's question takes a secret answer, which a chat message cannot carry.",
	}, err)
}
