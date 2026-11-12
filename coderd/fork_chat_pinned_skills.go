package coderd

// This file is fork code: it serves the pinned skills of a chat in the
// fork-pinned-skills experiment. registerForkChatRoutes mounts these handlers.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/skills"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// @Summary Get the pinned skills of a chat
// @ID get-the-pinned-skills-of-a-chat
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Success 200 {array} codersdk.ChatPinnedSkill
// @Router /api/v2/chats/fork/{chat}/pinned-skills [get]
// @x-apidocgen {"skip": true}
func (api *API) getChatPinnedSkills(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pins, err := readForkChatPinnedSkills(ctx, api.Database, httpmw.ChatParam(r).ID)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, pins)
}

// @Summary Replace the pinned skills of a chat
// @ID replace-the-pinned-skills-of-a-chat
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param request body codersdk.UpdateChatPinnedSkillsRequest true "Request body"
// @Success 200 {array} codersdk.ChatPinnedSkill
// @Router /api/v2/chats/fork/{chat}/pinned-skills [put]
// @x-apidocgen {"skip": true}
func (api *API) putChatPinnedSkills(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)
	if !api.authorizeForkPluginPacks(rw, r, policy.ActionUpdate, chat) {
		return
	}
	// Pinned skills run on the owner's turns with the owner's tools and
	// credentials, so only the owner may change them, as with sending
	// messages.
	if httpmw.APIKey(r).UserID != chat.OwnerID {
		httpapi.Write(ctx, rw, http.StatusForbidden, codersdk.Response{
			Message: "Only the chat owner may change pinned skills.",
		})
		return
	}
	var req codersdk.UpdateChatPinnedSkillsRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	if len(req.Skills) > chatd.MaxPinnedSkillsPerChat {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Too many pinned skills.",
			Detail:  fmt.Sprintf("A chat can pin at most %d skills.", chatd.MaxPinnedSkillsPerChat),
		})
		return
	}
	// Names are checked for shape only: chatd skips a pin the owner cannot
	// use when it builds the prompt, so a skill that is gone later needs no
	// cleanup here either.
	params := forkdb.InsertChatPinnedSkillsParams{ChatID: chat.ID}
	for i, pin := range req.Skills {
		switch pin.Source {
		case codersdk.ChatSkillSourcePersonal, codersdk.ChatSkillSourceOrganization,
			codersdk.ChatSkillSourceProject, codersdk.ChatSkillSourceWorkspace:
		default:
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message: "Invalid pinned skill.",
				Detail:  fmt.Sprintf("skills[%d].source %q is not supported.", i, pin.Source),
			})
			return
		}
		if !workspacesdk.SkillNamePattern.MatchString(pin.Name) || len(pin.Name) > skills.MaxPersonalSkillNameBytes {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message: "Invalid pinned skill.",
				Detail:  fmt.Sprintf("skills[%d].name must be a kebab-case skill name.", i),
			})
			return
		}
		params.SkillSources = append(params.SkillSources, string(pin.Source))
		params.SkillNames = append(params.SkillNames, pin.Name)
	}

	err := forkdb.InTx(api.Database, func(_ database.Store, ftx forkdb.Store) error {
		if err := ftx.DeleteChatPinnedSkills(ctx, chat.ID); err != nil {
			return err
		}
		if len(params.SkillNames) == 0 {
			return nil
		}
		return ftx.InsertChatPinnedSkills(ctx, params)
	}, nil)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	pins, err := readForkChatPinnedSkills(ctx, api.Database, chat.ID)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, pins)
}

// readForkChatPinnedSkills reads the pins of chatID as the actor in ctx, so
// dbauthz enforces read access to the chat. The result is never nil, so a
// response never writes null.
func readForkChatPinnedSkills(ctx context.Context, db database.Store, chatID uuid.UUID) ([]codersdk.ChatPinnedSkill, error) {
	fdb, err := forkdb.From(db)
	if err != nil {
		return nil, err
	}
	rows, err := fdb.GetChatPinnedSkillsByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	pins := make([]codersdk.ChatPinnedSkill, 0, len(rows))
	for _, row := range rows {
		pins = append(pins, codersdk.ChatPinnedSkill{Source: codersdk.ChatSkillSource(row.SkillSource), Name: row.SkillName})
	}
	return pins, nil
}
