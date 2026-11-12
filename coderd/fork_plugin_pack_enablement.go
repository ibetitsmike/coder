package coderd

// This file is fork code: it enables plugin packs for chat projects and chats
// in the fork-plugin-packs experiment. Enabling a pack mirrors enabling a
// skill, so it needs an update of the project or the chat and not a read of
// the pack. Organization admins choose which packs are installed, and the
// queries accept only a pack of the project's or the chat's organization.

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary List plugin packs available to enable
// @ID list-plugin-packs-available-to-enable
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Success 200 {array} codersdk.ForkEnabledPluginPack
// @Router /api/v2/chats/fork/organizations/{organization}/available-plugin-packs [get]
// @x-apidocgen {"skip": true}
func (api *API) forkAvailablePluginPacks(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	// Members enable packs but cannot read the organization skill object
	// packs reuse, so membership gates this projection instead. A
	// site-wide role can read every organization without being a member.
	if !api.isOrganizationMember(r, organization.ID) {
		httpapi.ResourceNotFound(rw)
		return
	}
	//nolint:gocritic // Member-safe projection of the organization's packs.
	rows, err := fdb.GetPluginPacksByOrganizationID(dbauthz.AsSystemRestricted(ctx), organization.ID)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	packs := make([]codersdk.ForkEnabledPluginPack, 0, len(rows))
	for _, row := range rows {
		packs = append(packs, codersdk.ForkEnabledPluginPack{ID: row.ID, Name: row.Name, CommitSHA: row.CommitSha})
	}
	httpapi.Write(ctx, rw, http.StatusOK, packs)
}

// @Summary List plugin packs of a chat project
// @ID list-plugin-packs-of-a-chat-project
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param project path string true "Chat project ID" format(uuid)
// @Success 200 {array} codersdk.ForkEnabledPluginPack
// @Router /api/v2/chats/fork/organizations/{organization}/projects/{project}/plugin-packs [get]
// @x-apidocgen {"skip": true}
func (api *API) forkProjectPluginPacks(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	rows, err := fdb.GetPluginPacksByProjectID(ctx, httpmw.ChatProjectParam(r).ID)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	packs := make([]codersdk.ForkEnabledPluginPack, 0, len(rows))
	for _, row := range rows {
		packs = append(packs, codersdk.ForkEnabledPluginPack{ID: row.ID, Name: row.Name, CommitSHA: row.CommitSha})
	}
	httpapi.Write(ctx, rw, http.StatusOK, packs)
}

// @Summary Enable a plugin pack for a chat project
// @ID enable-a-plugin-pack-for-a-chat-project
// @Security CoderSessionToken
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param project path string true "Chat project ID" format(uuid)
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/projects/{project}/plugin-packs/{pluginPack} [put]
// @x-apidocgen {"skip": true}
func (api *API) putForkProjectPluginPack(rw http.ResponseWriter, r *http.Request) {
	project := httpmw.ChatProjectParam(r)
	api.writeForkPluginPackEnablement(rw, r, project, func(fdb forkdb.Store, packID uuid.UUID) error {
		_, err := fdb.InsertPluginPackProject(r.Context(), forkdb.InsertPluginPackProjectParams{ProjectID: project.ID, PackID: packID})
		return err
	})
}

// @Summary Disable a plugin pack for a chat project
// @ID disable-a-plugin-pack-for-a-chat-project
// @Security CoderSessionToken
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param project path string true "Chat project ID" format(uuid)
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/projects/{project}/plugin-packs/{pluginPack} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkProjectPluginPack(rw http.ResponseWriter, r *http.Request) {
	project := httpmw.ChatProjectParam(r)
	api.writeForkPluginPackEnablement(rw, r, project, func(fdb forkdb.Store, packID uuid.UUID) error {
		return fdb.DeletePluginPackProject(r.Context(), forkdb.DeletePluginPackProjectParams{ProjectID: project.ID, PackID: packID})
	})
}

// @Summary List plugin packs of a chat
// @ID list-plugin-packs-of-a-chat
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param chat path string true "Chat ID" format(uuid)
// @Success 200 {array} codersdk.ForkEnabledPluginPack
// @Router /api/v2/chats/fork/{chat}/plugin-packs [get]
// @x-apidocgen {"skip": true}
func (api *API) forkChatPluginPacks(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	rows, err := fdb.GetPluginPacksByChatID(ctx, httpmw.ChatParam(r).ID)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	packs := make([]codersdk.ForkEnabledPluginPack, 0, len(rows))
	for _, row := range rows {
		packs = append(packs, codersdk.ForkEnabledPluginPack{ID: row.ID, Name: row.Name, CommitSHA: row.CommitSha})
	}
	httpapi.Write(ctx, rw, http.StatusOK, packs)
}

// @Summary Enable a plugin pack for a chat
// @ID enable-a-plugin-pack-for-a-chat
// @Security CoderSessionToken
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Success 204
// @Router /api/v2/chats/fork/{chat}/plugin-packs/{pluginPack} [put]
// @x-apidocgen {"skip": true}
func (api *API) putForkChatPluginPack(rw http.ResponseWriter, r *http.Request) {
	chat := httpmw.ChatParam(r)
	api.writeForkPluginPackEnablement(rw, r, chat, func(fdb forkdb.Store, packID uuid.UUID) error {
		_, err := fdb.InsertChatPluginPack(r.Context(), forkdb.InsertChatPluginPackParams{ChatID: chat.ID, PackID: packID})
		return err
	})
}

// @Summary Disable a plugin pack for a chat
// @ID disable-a-plugin-pack-for-a-chat
// @Security CoderSessionToken
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Success 204
// @Router /api/v2/chats/fork/{chat}/plugin-packs/{pluginPack} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkChatPluginPack(rw http.ResponseWriter, r *http.Request) {
	chat := httpmw.ChatParam(r)
	api.writeForkPluginPackEnablement(rw, r, chat, func(fdb forkdb.Store, packID uuid.UUID) error {
		return fdb.DeleteChatPluginPack(r.Context(), forkdb.DeleteChatPluginPackParams{ChatID: chat.ID, PackID: packID})
	})
}

// @Summary List plugin packs turned off for a chat
// @ID list-plugin-packs-turned-off-for-a-chat
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param chat path string true "Chat ID" format(uuid)
// @Success 200 {array} codersdk.ForkEnabledPluginPack
// @Router /api/v2/chats/fork/{chat}/excluded-plugin-packs [get]
// @x-apidocgen {"skip": true}
func (api *API) forkChatExcludedPluginPacks(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	rows, err := fdb.GetExcludedPluginPacksByChatID(ctx, httpmw.ChatParam(r).ID)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	packs := make([]codersdk.ForkEnabledPluginPack, 0, len(rows))
	for _, row := range rows {
		packs = append(packs, codersdk.ForkEnabledPluginPack{ID: row.ID, Name: row.Name, CommitSHA: row.CommitSha})
	}
	httpapi.Write(ctx, rw, http.StatusOK, packs)
}

// @Summary Turn off a plugin pack for a chat
// @Description The pack stays off for the chat and its subagents even when the chat's project or the chat enables it.
// @ID turn-off-a-plugin-pack-for-a-chat
// @Security CoderSessionToken
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Success 204
// @Router /api/v2/chats/fork/{chat}/excluded-plugin-packs/{pluginPack} [put]
// @x-apidocgen {"skip": true}
func (api *API) putForkChatExcludedPluginPack(rw http.ResponseWriter, r *http.Request) {
	chat := httpmw.ChatParam(r)
	api.writeForkPluginPackEnablement(rw, r, chat, func(fdb forkdb.Store, packID uuid.UUID) error {
		_, err := fdb.InsertChatExcludedPluginPack(r.Context(), forkdb.InsertChatExcludedPluginPackParams{ChatID: chat.ID, PackID: packID})
		return err
	})
}

// @Summary Stop turning off a plugin pack for a chat
// @ID stop-turning-off-a-plugin-pack-for-a-chat
// @Security CoderSessionToken
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Success 204
// @Router /api/v2/chats/fork/{chat}/excluded-plugin-packs/{pluginPack} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkChatExcludedPluginPack(rw http.ResponseWriter, r *http.Request) {
	chat := httpmw.ChatParam(r)
	api.writeForkPluginPackEnablement(rw, r, chat, func(fdb forkdb.Store, packID uuid.UUID) error {
		return fdb.DeleteChatExcludedPluginPack(r.Context(), forkdb.DeleteChatExcludedPluginPackParams{ChatID: chat.ID, PackID: packID})
	})
}

// writeForkPluginPackEnablement authorizes an update of target, then runs
// write for the {pluginPack} parameter. A pack that does not exist or is in
// another organization answers 404, like a pack the user cannot see.
func (api *API) writeForkPluginPackEnablement(rw http.ResponseWriter, r *http.Request, target rbac.Objecter, write func(forkdb.Store, uuid.UUID) error) {
	packID, ok := httpmw.ParseUUIDParam(rw, r, "pluginPack")
	if !ok {
		return
	}
	if !api.authorizeForkPluginPacks(rw, r, policy.ActionUpdate, target) {
		return
	}
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	if err := write(fdb, packID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.ResourceNotFound(rw)
			return
		}
		httpapi.InternalServerError(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}
