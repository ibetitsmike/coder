package coderd

// This file is fork code: it serves the chat family capacity of the
// fork-nested-subagents experiment. registerForkChatRoutes mounts the handler.

import (
	"net/http"

	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary Get the subagent capacity of a chat family
// @ID get-the-subagent-capacity-of-a-chat-family
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Root chat ID" format(uuid)
// @Success 200 {object} codersdk.ForkChatFamilyCapacity
// @Router /api/v2/chats/fork/{chat}/family [get]
// @x-apidocgen {"skip": true}
// @Description Returns the live subagent count and limit of the family under
// @Description a root chat, and the running subagents that wait for a
// @Description concurrency slot.
func (api *API) getForkChatFamilyCapacity(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)
	if chat.ParentChatID.Valid {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Only a root chat has a family capacity.",
		})
		return
	}
	if !api.requireChatDaemon(ctx, rw) {
		return
	}

	family, err := api.chatDaemon.ForkChatFamilyCapacity(ctx, chat)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to get the chat family capacity.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, family)
}
