package coderd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/coderd/x/plugins"
	"github.com/coder/coder/v2/codersdk"
)

const (
	// maxPluginModuleBytes caps one Wasm module. A Go plugin is
	// approximately 5 to 30 MiB.
	maxPluginModuleBytes = 48 << 20
	pluginLinkCodeTTL    = 10 * time.Minute
	pluginWasmMimeType   = "application/wasm"
	// Crockford-like alphabet without 0, 1, I, L, O and U, so a code read
	// aloud or typed on a phone has no look-alike characters.
	pluginLinkCodeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"
)

func (api *API) pluginsUnavailable(rw http.ResponseWriter) bool {
	if api.pluginManager == nil {
		httpapi.Write(api.ctx, rw, http.StatusNotFound, codersdk.Response{
			Message: "Plugins are not enabled.",
		})
		return true
	}
	return false
}

// @Summary Inspect a plugin module
// @ID inspect-plugin-module
// @Security CoderSessionToken
// @Accept application/wasm
// @Produce json
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Success 200 {object} codersdk.InspectPluginResponse
// @Router /api/experimental/organizations/{organization}/plugins/inspect [post]
// @x-apidocgen {"skip": true}
func (api *API) postOrganizationPluginInspect(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := httpmw.OrganizationParam(r)
	if api.pluginsUnavailable(rw) {
		return
	}
	if !api.Authorize(r, policy.ActionCreate, rbac.ResourcePlugin.InOrg(org.ID)) {
		httpapi.Forbidden(rw)
		return
	}
	wasm, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, maxPluginModuleBytes))
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusRequestEntityTooLarge, codersdk.Response{
			Message: "Module is too large or the body cannot be read.",
			Detail:  err.Error(),
		})
		return
	}
	manifest, ok := api.inspectPluginModule(rw, r, wasm)
	if !ok {
		return
	}
	sum := sha256.Sum256(wasm)
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.InspectPluginResponse{
		Manifest:  manifest,
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: len(wasm),
	})
}

func (api *API) inspectPluginModule(rw http.ResponseWriter, r *http.Request, wasm []byte) (codersdk.PluginManifest, bool) {
	ctx := r.Context()
	if len(wasm) == 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "Module is empty."})
		return codersdk.PluginManifest{}, false
	}
	manifest, err := api.pluginManager.Inspect(ctx, wasm)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "The module cannot be loaded.",
			Detail:  err.Error(),
		})
		return codersdk.PluginManifest{}, false
	}
	if err := plugins.ValidateManifest(manifest); err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "The module manifest is not valid.",
			Detail:  err.Error(),
		})
		return codersdk.PluginManifest{}, false
	}
	return manifest, true
}

// @Summary Install a plugin
// @ID install-plugin
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param request body codersdk.CreatePluginRequest true "Install request"
// @Success 201 {object} codersdk.Plugin
// @Router /api/experimental/organizations/{organization}/plugins [post]
// @x-apidocgen {"skip": true}
func (api *API) postOrganizationPlugin(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := httpmw.OrganizationParam(r)
	apiKey := httpmw.APIKey(r)
	if api.pluginsUnavailable(rw) {
		return
	}
	if !api.Authorize(r, policy.ActionCreate, rbac.ResourcePlugin.InOrg(org.ID)) {
		httpapi.Forbidden(rw)
		return
	}
	var req codersdk.CreatePluginRequest
	// Base64 makes the JSON body 4/3 of the module size.
	if !httpapi.ReadLimit(ctx, rw, r, maxPluginModuleBytes/3*4+(1<<20), &req) {
		return
	}
	manifest, ok := api.inspectPluginModule(rw, r, req.Wasm)
	if !ok {
		return
	}
	if err := plugins.ValidateGrants(manifest, req.Grants); err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Grants must equal the manifest of the module.",
			Detail:  err.Error(),
		})
		return
	}
	config, ok := pluginConfigJSON(rw, r, manifest, req.Config)
	if !ok {
		return
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	grantsJSON, err := json.Marshal(req.Grants)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}

	sum := sha256.Sum256(req.Wasm)
	hash := hex.EncodeToString(sum[:])
	var plugin forkdb.Plugin
	err = forkdb.InTx(api.Database, func(tx database.Store, ftx forkdb.Store) error {
		file, err := tx.GetFileByHashAndCreator(ctx, database.GetFileByHashAndCreatorParams{
			Hash:      hash,
			CreatedBy: apiKey.UserID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			file, err = tx.InsertFile(ctx, database.InsertFileParams{
				ID:        uuid.New(),
				Hash:      hash,
				CreatedBy: apiKey.UserID,
				CreatedAt: dbtime.Now(),
				Mimetype:  pluginWasmMimeType,
				Data:      req.Wasm,
			})
		}
		if err != nil {
			return err
		}
		now := dbtime.Now()
		plugin, err = ftx.InsertPlugin(ctx, forkdb.InsertPluginParams{
			ID:             uuid.New(),
			OrganizationID: org.ID,
			Name:           manifest.Name,
			Version:        manifest.Version,
			FileID:         file.ID,
			Manifest:       manifestJSON,
			Grants:         grantsJSON,
			Config:         config,
			// ConfigKeyID is set by the dbcrypt wrapper.
			ConfigKeyID: sql.NullString{},
			Enabled:     true,
			CreatedBy:   apiKey.UserID,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		return err
	}, nil)
	if database.IsUniqueViolation(err) {
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "A plugin with this name is already installed in the organization.",
		})
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	api.publishPluginsChanged()
	writePlugin(ctx, rw, http.StatusCreated, plugin)
}

// pluginConfigJSON validates config values against the manifest and
// returns the JSON to store.
func pluginConfigJSON(rw http.ResponseWriter, r *http.Request, manifest codersdk.PluginManifest, config map[string]string) (string, bool) {
	if len(config) == 0 {
		return "", true
	}
	for k := range config {
		if !slices.Contains(manifest.ConfigKeys, k) {
			httpapi.Write(r.Context(), rw, http.StatusBadRequest, codersdk.Response{
				Message: "Config key is not in the manifest.",
				Validations: []codersdk.ValidationError{{
					Field:  "config." + k,
					Detail: "The manifest does not request this key.",
				}},
			})
			return "", false
		}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return "", false
	}
	return string(raw), true
}

// @Summary List plugins of an organization
// @ID list-organization-plugins
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Success 200 {array} codersdk.Plugin
// @Router /api/experimental/organizations/{organization}/plugins [get]
// @x-apidocgen {"skip": true}
//
//nolint:revive // get-return: revive assumes get* must be a getter, but this is an HTTP handler.
func (api *API) getOrganizationPlugins(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := httpmw.OrganizationParam(r)
	if !api.Authorize(r, policy.ActionRead, rbac.ResourcePlugin.InOrg(org.ID)) {
		httpapi.Forbidden(rw)
		return
	}
	rows, err := api.pluginDB.GetPluginsByOrganizationID(ctx, org.ID)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	out := make([]codersdk.Plugin, 0, len(rows))
	for _, p := range rows {
		converted, err := convertPlugin(p)
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		out = append(out, converted)
	}
	httpapi.Write(ctx, rw, http.StatusOK, out)
}

// pluginParam reads the {plugin} route parameter. It reads the row as the
// system, so each handler must make its own authorization check.
func (api *API) pluginParam(rw http.ResponseWriter, r *http.Request) (forkdb.Plugin, bool) {
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "plugin"))
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "Plugin ID must be a UUID."})
		return forkdb.Plugin{}, false
	}
	//nolint:gocritic // Members link to plugins they cannot read; handlers authorize.
	p, err := api.pluginDB.GetPluginByID(dbauthz.AsSystemRestricted(ctx), id)
	if httpapi.Is404Error(err) {
		httpapi.ResourceNotFound(rw)
		return forkdb.Plugin{}, false
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return forkdb.Plugin{}, false
	}
	return p, true
}

// ownedPlugin reads the {plugin} parameter and requires the action on it.
// A caller without read access gets 404.
func (api *API) ownedPlugin(rw http.ResponseWriter, r *http.Request, action policy.Action) (forkdb.Plugin, bool) {
	p, ok := api.pluginParam(rw, r)
	if !ok {
		return forkdb.Plugin{}, false
	}
	if !api.Authorize(r, policy.ActionRead, p) {
		httpapi.ResourceNotFound(rw)
		return forkdb.Plugin{}, false
	}
	if action != policy.ActionRead && !api.Authorize(r, action, p) {
		httpapi.Forbidden(rw)
		return forkdb.Plugin{}, false
	}
	return p, true
}

// @Summary Get a plugin
// @ID get-plugin
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param plugin path string true "Plugin ID" format(uuid)
// @Success 200 {object} codersdk.Plugin
// @Router /api/experimental/plugins/{plugin} [get]
// @x-apidocgen {"skip": true}
//
//nolint:revive // get-return: revive assumes get* must be a getter, but this is an HTTP handler.
func (api *API) getPlugin(rw http.ResponseWriter, r *http.Request) {
	p, ok := api.ownedPlugin(rw, r, policy.ActionRead)
	if !ok {
		return
	}
	writePlugin(r.Context(), rw, http.StatusOK, p)
}

// @Summary Update a plugin
// @ID update-plugin
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags Chats
// @Param plugin path string true "Plugin ID" format(uuid)
// @Param request body codersdk.UpdatePluginRequest true "Update request"
// @Success 200 {object} codersdk.Plugin
// @Router /api/experimental/plugins/{plugin} [patch]
// @x-apidocgen {"skip": true}
func (api *API) patchPlugin(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := api.ownedPlugin(rw, r, policy.ActionUpdate)
	if !ok {
		return
	}
	var req codersdk.UpdatePluginRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	var manifest codersdk.PluginManifest
	if err := json.Unmarshal(p.Manifest, &manifest); err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	var config string
	if req.Config != nil {
		if config, ok = pluginConfigJSON(rw, r, manifest, *req.Config); !ok {
			return
		}
	}
	err := forkdb.InTx(api.Database, func(_ database.Store, ftx forkdb.Store) error {
		var err error
		if req.Config != nil {
			p, err = ftx.UpdatePluginConfig(ctx, forkdb.UpdatePluginConfigParams{
				ID:     p.ID,
				Config: config,
				// ConfigKeyID is set by the dbcrypt wrapper.
				ConfigKeyID: sql.NullString{},
				UpdatedAt:   dbtime.Now(),
			})
			if err != nil {
				return err
			}
		}
		if req.Enabled != nil {
			p, err = ftx.UpdatePluginEnabled(ctx, forkdb.UpdatePluginEnabledParams{
				ID:        p.ID,
				Enabled:   *req.Enabled,
				UpdatedAt: dbtime.Now(),
			})
		}
		return err
	}, nil)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	api.publishPluginsChanged()
	writePlugin(ctx, rw, http.StatusOK, p)
}

// @Summary Delete a plugin
// @ID delete-plugin
// @Security CoderSessionToken
// @Tags Chats
// @Param plugin path string true "Plugin ID" format(uuid)
// @Success 204
// @Router /api/experimental/plugins/{plugin} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deletePlugin(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := api.ownedPlugin(rw, r, policy.ActionDelete)
	if !ok {
		return
	}
	if err := api.pluginDB.DeletePluginByID(ctx, p.ID); err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	api.publishPluginsChanged()
	rw.WriteHeader(http.StatusNoContent)
}

// @Summary List the links of a plugin
// @ID list-plugin-links
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param plugin path string true "Plugin ID" format(uuid)
// @Success 200 {array} codersdk.PluginUserLink
// @Router /api/experimental/plugins/{plugin}/links [get]
// @x-apidocgen {"skip": true}
//
//nolint:revive // get-return: revive assumes get* must be a getter, but this is an HTTP handler.
func (api *API) getPluginLinks(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := api.ownedPlugin(rw, r, policy.ActionRead)
	if !ok {
		return
	}
	//nolint:gocritic // The caller can read the plugin; links are system rows.
	links, err := api.pluginDB.GetPluginUserLinksByPluginID(dbauthz.AsSystemRestricted(ctx), p.ID)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertPluginLinks(links))
}

// linkablePlugin reports whether a member can link to the plugin: it runs
// in an organization of the member and acts in chats.
func (api *API) linkablePlugin(r *http.Request, p forkdb.Plugin) bool {
	if !p.Enabled {
		return false
	}
	var grants codersdk.PluginGrants
	if err := json.Unmarshal(p.Grants, &grants); err != nil || len(grants.Chats) == 0 {
		return false
	}
	return api.isOrganizationMember(r, p.OrganizationID)
}

func (api *API) isOrganizationMember(r *http.Request, orgID uuid.UUID) bool {
	apiKey := httpmw.APIKey(r)
	//nolint:gocritic // Read the memberships of the caller.
	rows, err := api.Database.GetOrganizationIDsByMemberIDs(dbauthz.AsSystemRestricted(r.Context()), []uuid.UUID{apiKey.UserID})
	if err != nil {
		return false
	}
	for _, row := range rows {
		if slices.Contains(row.OrganizationIDs, orgID) {
			return true
		}
	}
	return false
}

// @Summary Create a plugin link code
// @ID create-plugin-link-code
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param plugin path string true "Plugin ID" format(uuid)
// @Success 201 {object} codersdk.PluginLinkCode
// @Router /api/experimental/plugins/{plugin}/link-codes [post]
// @x-apidocgen {"skip": true}
func (api *API) postPluginLinkCode(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKey := httpmw.APIKey(r)
	p, ok := api.pluginParam(rw, r)
	if !ok {
		return
	}
	if !api.linkablePlugin(r, p) {
		httpapi.ResourceNotFound(rw)
		return
	}
	code, err := newPluginLinkCode()
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	expiresAt := dbtime.Now().Add(pluginLinkCodeTTL)
	//nolint:gocritic // Link codes are system rows; the caller is a member.
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	err = forkdb.InTx(api.Database, func(_ database.Store, ftx forkdb.Store) error {
		err := ftx.DeletePluginLinkCodesByUser(sysCtx, forkdb.DeletePluginLinkCodesByUserParams{
			PluginID: p.ID,
			UserID:   apiKey.UserID,
		})
		if err != nil {
			return err
		}
		return ftx.InsertPluginLinkCode(sysCtx, forkdb.InsertPluginLinkCodeParams{
			CodeHash:  plugins.HashLinkCode(code),
			PluginID:  p.ID,
			UserID:    apiKey.UserID,
			ExpiresAt: expiresAt,
		})
	}, nil)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusCreated, codersdk.PluginLinkCode{Code: code, ExpiresAt: expiresAt})
}

// newPluginLinkCode returns a code such as "K7Q-2MX". A plugin gets five
// guesses a minute, so 30 bits for 10 minutes is enough.
func newPluginLinkCode() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	out := make([]byte, 0, 7)
	for i, v := range b {
		if i == 3 {
			out = append(out, '-')
		}
		// 256 is not a multiple of 30; the bias is too small to matter
		// for a code that works once, for 10 minutes.
		out = append(out, pluginLinkCodeAlphabet[int(v)%len(pluginLinkCodeAlphabet)])
	}
	return string(out), nil
}

// @Summary Delete a plugin link
// @ID delete-plugin-link
// @Security CoderSessionToken
// @Tags Chats
// @Param plugin path string true "Plugin ID" format(uuid)
// @Param externalID path string true "External ID"
// @Success 204
// @Router /api/experimental/plugins/{plugin}/links/{externalID} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deletePluginLink(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKey := httpmw.APIKey(r)
	p, ok := api.pluginParam(rw, r)
	if !ok {
		return
	}
	//nolint:gocritic // Links are system rows; ownership is checked below.
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	link, err := api.pluginDB.GetPluginUserLink(sysCtx, forkdb.GetPluginUserLinkParams{
		PluginID:   p.ID,
		ExternalID: chi.URLParam(r, "externalID"),
	})
	if httpapi.Is404Error(err) {
		httpapi.ResourceNotFound(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	if link.UserID != apiKey.UserID && !api.Authorize(r, policy.ActionUpdate, p) {
		httpapi.ResourceNotFound(rw)
		return
	}
	err = api.pluginDB.DeletePluginUserLink(sysCtx, forkdb.DeletePluginUserLinkParams{
		PluginID:   p.ID,
		ExternalID: link.ExternalID,
	})
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// @Summary List plugins the user can link to
// @ID list-linkable-plugins
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Success 200 {array} codersdk.LinkablePlugin
// @Router /api/experimental/users/me/plugins [get]
// @x-apidocgen {"skip": true}
//
//nolint:revive // get-return: revive assumes get* must be a getter, but this is an HTTP handler.
func (api *API) getLinkablePlugins(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKey := httpmw.APIKey(r)
	//nolint:gocritic // Members list plugins they can link to, not the full rows.
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	orgs, err := api.Database.GetOrganizationsByUserID(sysCtx, database.GetOrganizationsByUserIDParams{
		UserID:  apiKey.UserID,
		Deleted: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpapi.InternalServerError(rw, err)
		return
	}
	links, err := api.pluginDB.GetPluginUserLinksByUserID(sysCtx, apiKey.UserID)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	linksByPlugin := map[uuid.UUID][]forkdb.PluginUserLink{}
	for _, l := range links {
		linksByPlugin[l.PluginID] = append(linksByPlugin[l.PluginID], l)
	}
	out := []codersdk.LinkablePlugin{}
	for _, org := range orgs {
		rows, err := api.pluginDB.GetPluginsByOrganizationID(sysCtx, org.ID)
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		for _, p := range rows {
			var grants codersdk.PluginGrants
			if !p.Enabled || json.Unmarshal(p.Grants, &grants) != nil || len(grants.Chats) == 0 {
				continue
			}
			out = append(out, codersdk.LinkablePlugin{
				ID:               p.ID,
				OrganizationID:   p.OrganizationID,
				OrganizationName: org.Name,
				Name:             p.Name,
				Version:          p.Version,
				Chats:            grants.Chats,
				Links:            convertPluginLinks(linksByPlugin[p.ID]),
			})
		}
	}
	httpapi.Write(ctx, rw, http.StatusOK, out)
}

// @Summary Send a webhook to a plugin
// @ID plugin-webhook
// @Tags Chats
// @Param plugin path string true "Plugin ID" format(uuid)
// @Success 200
// @Router /api/experimental/plugins/{plugin}/webhook [post]
// @x-apidocgen {"skip": true}
func (api *API) postPluginWebhook(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if api.pluginsUnavailable(rw) {
		return
	}
	p, ok := api.pluginParam(rw, r)
	if !ok {
		return
	}
	var grants codersdk.PluginGrants
	if !p.Enabled || json.Unmarshal(p.Grants, &grants) != nil || !grants.Webhook {
		httpapi.ResourceNotFound(rw)
		return
	}
	resp, err := api.pluginManager.Webhook(ctx, p, r)
	if errors.Is(err, plugins.ErrNotRunningHere) {
		// Senders such as Telegram retry a failed webhook.
		rw.Header().Set("Retry-After", "5")
		httpapi.Write(ctx, rw, http.StatusServiceUnavailable, codersdk.Response{
			Message: "The plugin does not run on this replica.",
		})
		return
	}
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadGateway, codersdk.Response{
			Message: "The plugin failed to handle the webhook.",
			Detail:  err.Error(),
		})
		return
	}
	// Other headers could set cookies on the Coder origin.
	for _, name := range []string{"Content-Type", "Retry-After"} {
		if v := resp.Header.Get(name); v != "" {
			rw.Header().Set(name, v)
		}
	}
	rw.WriteHeader(resp.StatusCode)
	if _, err := rw.Write(resp.Body); err != nil {
		api.Logger.Debug(ctx, "write plugin webhook response", slog.Error(err))
	}
}

func (api *API) publishPluginsChanged() {
	if err := api.Pubsub.Publish(plugins.ChangedChannel, nil); err != nil {
		api.Logger.Warn(api.ctx, "publish plugin change", slog.Error(err))
	}
}

func writePlugin(ctx context.Context, rw http.ResponseWriter, status int, p forkdb.Plugin) {
	out, err := convertPlugin(p)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, status, out)
}

func convertPlugin(p forkdb.Plugin) (codersdk.Plugin, error) {
	out := codersdk.Plugin{
		ID:             p.ID,
		OrganizationID: p.OrganizationID,
		Name:           p.Name,
		Version:        p.Version,
		FileID:         p.FileID,
		Enabled:        p.Enabled,
		LastError:      p.LastError,
		CreatedBy:      p.CreatedBy,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
		ConfigKeysSet:  []string{},
	}
	if err := json.Unmarshal(p.Manifest, &out.Manifest); err != nil {
		return codersdk.Plugin{}, xerrors.Errorf("decode plugin manifest: %w", err)
	}
	if err := json.Unmarshal(p.Grants, &out.Grants); err != nil {
		return codersdk.Plugin{}, xerrors.Errorf("decode plugin grants: %w", err)
	}
	if p.Config != "" {
		var config map[string]string
		if json.Unmarshal([]byte(p.Config), &config) == nil {
			out.ConfigKeysSet = slices.Sorted(maps.Keys(config))
		}
	}
	if p.RunnerID.Valid && p.LeaseExpiresAt.Valid && p.LeaseExpiresAt.Time.After(dbtime.Now()) {
		id := p.RunnerID.UUID
		out.RunnerID = &id
	}
	return out, nil
}

func convertPluginLinks(links []forkdb.PluginUserLink) []codersdk.PluginUserLink {
	out := make([]codersdk.PluginUserLink, 0, len(links))
	for _, l := range links {
		out = append(out, codersdk.PluginUserLink{
			PluginID:   l.PluginID,
			ExternalID: l.ExternalID,
			UserID:     l.UserID,
			CreatedAt:  l.CreatedAt,
		})
	}
	return out
}
