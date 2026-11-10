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
// @Description says only that the user answered.
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
	if strings.TrimSpace(req.Value) == "" {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "The answer must not be empty."})
		return
	}
	if len(req.Value) < minSecretAnswerLength {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: fmt.Sprintf("Secret answers must be at least %d bytes so they can be hidden from tool output. "+
				"Answer without the secret option if the value isn't sensitive.", minSecretAnswerLength),
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

	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	question, err := fdb.GetPendingChatDescendantQuestion(ctx, chat.ID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && question.ToolCallID != toolCallID) {
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

	// Errors stay out of the responses below: a database error can quote
	// the value.
	insert := forkdb.InsertChatSecretAnswerParams{
		ChatID:     chat.ID,
		ToolCallID: toolCallID,
		Value:      req.Value,
	}
	err = fdb.InsertChatSecretAnswer(ctx, insert)
	if database.IsUniqueViolation(err) {
		err = replaceUnreportedSecretAnswer(ctx, api.Database, insert)
		if errors.Is(err, errSecretAnswerReported) || database.IsUniqueViolation(err) {
			httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{Message: "The question already has an answer."})
			return
		}
	}
	if err != nil {
		api.Logger.Error(ctx, "insert secret answer", slog.F("chat_id", chat.ID))
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{Message: "Failed to store the answer."})
		return
	}

	_, err = api.sendChatMessage(ctx, apiKey.UserID, chat, codersdk.CreateChatMessageRequest{
		Content: []codersdk.ChatInputPart{{
			Type: codersdk.ChatInputPartTypeText,
			Text: questionAnswerPrefix + toolCallID + ":\nThe user gave a secret answer. Write {{secret:" + toolCallID + "}} where the value belongs in execute, write_file or edit_files input.",
		}},
	})
	if err != nil {
		// The answer stays stored. The question stays pending without the
		// message, and the next answer replaces it as an unreported one.
		writeChatError(ctx, rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// errSecretAnswerReported means that a queued or delivered message
// reports the stored answer.
var errSecretAnswerReported = xerrors.New("the secret answer is reported")

// replaceUnreportedSecretAnswer replaces the stored answer to the question
// of arg.ToolCallID when no message reports it: the owner can delete the
// queued message that reports an answer, and a failed send leaves none.
// The chat row lock serializes it with the queue's promotion, so the
// message cannot be delivered between the check and the replacement.
func replaceUnreportedSecretAnswer(ctx context.Context, db database.Store, arg forkdb.InsertChatSecretAnswerParams) error {
	return forkdb.InTx(db, func(tx database.Store, ftx forkdb.Store) error {
		if _, err := tx.GetChatByIDForUpdate(ctx, arg.ChatID); err != nil {
			return err
		}
		// A delivered message answers the question.
		question, err := ftx.GetPendingChatDescendantQuestion(ctx, arg.ChatID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && question.ToolCallID != arg.ToolCallID) {
			return errSecretAnswerReported
		}
		if err != nil {
			return err
		}
		queued, err := secretAnswerQueued(ctx, tx, arg.ChatID, arg.ToolCallID)
		if err != nil {
			return err
		}
		if queued {
			// Reads skip an answer past its TTL before dbpurge deletes
			// it, so an expired answer is replaced like an unreported one.
			queued, err = secretAnswerLive(ctx, ftx, arg.ChatID, arg.ToolCallID)
			if err != nil {
				return err
			}
		}
		if queued {
			return errSecretAnswerReported
		}
		if err := ftx.DeleteChatSecretAnswer(ctx, forkdb.DeleteChatSecretAnswerParams{ChatID: arg.ChatID, ToolCallID: arg.ToolCallID}); err != nil {
			return err
		}
		return ftx.InsertChatSecretAnswer(ctx, arg)
	}, nil)
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
	return httperror.NewResponseError(http.StatusConflict, codersdk.Response{
		Message: "The chat's question takes a secret answer, which a chat message cannot carry.",
	})
}
