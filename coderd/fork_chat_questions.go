package coderd

// This file is fork code: it lists the descendant questions of the
// fork-nested-subagents experiment. registerForkChatRoutes mounts the handler.

import (
	"encoding/json"
	"net/http"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/codersdk"
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
