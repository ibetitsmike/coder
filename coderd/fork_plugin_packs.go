package coderd

// This file is fork code: it installs plugin packs from Git for the
// fork-plugin-packs experiment. Packs reuse the organization skill RBAC
// object, so only organization admins manage them and auditors read them.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/coderd/x/pluginpacks"
	"github.com/coder/coder/v2/codersdk"
)

const (
	maxForkPluginPackSourceHosts = 50
	forkPluginPackFetchTimeout   = 2 * time.Minute
)

// @Summary Get plugin pack sources
// @ID get-plugin-pack-sources
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Success 200 {object} codersdk.ForkPluginPackSources
// @Router /api/v2/chats/fork/organizations/{organization}/plugin-pack-sources [get]
// @x-apidocgen {"skip": true}
func (api *API) forkPluginPackSources(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	hosts, err := forkPluginPackSourceHosts(ctx, fdb, httpmw.OrganizationParam(r).ID)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.ForkPluginPackSources{Hosts: hosts})
}

// @Summary Update plugin pack sources
// @ID update-plugin-pack-sources
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param request body codersdk.ForkPluginPackSources true "Request body"
// @Success 200 {object} codersdk.ForkPluginPackSources
// @Router /api/v2/chats/fork/organizations/{organization}/plugin-pack-sources [put]
// @x-apidocgen {"skip": true}
func (api *API) updateForkPluginPackSources(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !api.authorizeForkPluginPacks(rw, r, policy.ActionUpdate, rbac.ResourceOrganizationSkill.InOrg(httpmw.OrganizationParam(r).ID)) {
		return
	}
	var req codersdk.ForkPluginPackSources
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	hosts := make([]string, 0, len(req.Hosts))
	for _, host := range req.Hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || len(host) > 253 || strings.ContainsAny(host, "/:@?# ") {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
				Message: "Invalid plugin pack source host.",
				Detail:  fmt.Sprintf("%q must be a host name without a scheme, port or path.", host),
			})
			return
		}
		if !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) > maxForkPluginPackSourceHosts {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: fmt.Sprintf("At most %d plugin pack source hosts are allowed.", maxForkPluginPackSourceHosts),
		})
		return
	}
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	row, err := fdb.UpsertPluginPackSources(ctx, forkdb.UpsertPluginPackSourcesParams{
		OrganizationID: httpmw.OrganizationParam(r).ID,
		Hosts:          hosts,
		UpdatedAt:      dbtime.Now(),
	})
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.ForkPluginPackSources{Hosts: row.Hosts})
}

// @Summary List plugin packs
// @ID list-plugin-packs
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Success 200 {array} codersdk.ForkPluginPack
// @Router /api/v2/chats/fork/organizations/{organization}/plugin-packs [get]
// @x-apidocgen {"skip": true}
func (api *API) forkPluginPacks(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	rows, err := fdb.GetPluginPacksByOrganizationID(ctx, httpmw.OrganizationParam(r).ID)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	packs := make([]codersdk.ForkPluginPack, 0, len(rows))
	for _, row := range rows {
		packs = append(packs, convertForkPluginPack(row, nil))
	}
	httpapi.Write(ctx, rw, http.StatusOK, packs)
}

// @Summary Get a plugin pack
// @ID get-a-plugin-pack
// @Security CoderSessionToken
// @Tags Chats
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Success 200 {object} codersdk.ForkPluginPack
// @Router /api/v2/chats/fork/organizations/{organization}/plugin-packs/{pluginPack} [get]
// @x-apidocgen {"skip": true}
func (api *API) forkPluginPack(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, pack, ok := api.forkPluginPackParam(rw, r)
	if !ok {
		return
	}
	resources, err := fdb.GetPluginPackResources(ctx, pack.ID)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, convertForkPluginPack(pack, resources))
}

// @Summary Install a plugin pack
// @ID install-a-plugin-pack
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param request body codersdk.CreateForkPluginPackRequest true "Request body"
// @Success 201 {object} codersdk.ForkPluginPack
// @Router /api/v2/chats/fork/organizations/{organization}/plugin-packs [post]
// @x-apidocgen {"skip": true}
func (api *API) postForkPluginPack(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	organization := httpmw.OrganizationParam(r)
	// Authorize before the fetch, so a user who cannot install never makes
	// the server fetch a repository.
	if !api.authorizeForkPluginPacks(rw, r, policy.ActionCreate, rbac.ResourceOrganizationSkill.InOrg(organization.ID)) {
		return
	}
	var req codersdk.CreateForkPluginPackRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	if err := codersdk.NameValid(req.Name); err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message:     "Invalid plugin pack name.",
			Validations: []codersdk.ValidationError{{Field: "name", Detail: err.Error()}},
		})
		return
	}
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return
	}
	src := pluginpacks.Source{URL: req.SourceURL, Path: req.SourcePath, Ref: req.Ref}
	snap, ok := api.fetchForkPluginPack(ctx, rw, fdb, organization.ID, src)
	if !ok {
		return
	}
	var pack forkdb.PluginPack
	err := forkdb.InTx(api.Database, func(_ database.Store, ftx forkdb.Store) error {
		now := dbtime.Now()
		var err error
		pack, err = ftx.InsertPluginPack(ctx, forkdb.InsertPluginPackParams{
			ID:             uuid.New(),
			OrganizationID: organization.ID,
			Name:           req.Name,
			SourceUrl:      req.SourceURL,
			SourcePath:     req.SourcePath,
			Ref:            req.Ref,
			CommitSha:      snap.CommitSHA,
			CreatedBy:      httpmw.APIKey(r).UserID,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if err != nil {
			return err
		}
		return insertForkPluginPackFiles(ctx, ftx, pack.ID, snap.Files)
	}, nil)
	if database.IsUniqueViolation(err, "plugin_packs_organization_id_name_key") {
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{Message: "A plugin pack with this name already exists."})
		return
	}
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	api.writeForkPluginPack(ctx, rw, fdb, pack, http.StatusCreated)
}

// @Summary Update a plugin pack
// @ID update-a-plugin-pack
// @Security CoderSessionToken
// @Tags Chats
// @Accept json
// @Produce json
// @Param organization path string true "Organization ID or name"
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Param request body codersdk.UpdateForkPluginPackRequest true "Request body"
// @Success 200 {object} codersdk.ForkPluginPack
// @Router /api/v2/chats/fork/organizations/{organization}/plugin-packs/{pluginPack}/update [post]
// @x-apidocgen {"skip": true}
func (api *API) updateForkPluginPack(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, pack, ok := api.forkPluginPackParam(rw, r)
	if !ok {
		return
	}
	if !api.authorizeForkPluginPacks(rw, r, policy.ActionUpdate, pack) {
		return
	}
	var req codersdk.UpdateForkPluginPackRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	ref := pack.Ref
	if req.Ref != "" {
		ref = req.Ref
	}
	src := pluginpacks.Source{URL: pack.SourceUrl, Path: pack.SourcePath, Ref: ref}
	snap, ok := api.fetchForkPluginPack(ctx, rw, fdb, pack.OrganizationID, src)
	if !ok {
		return
	}
	err := forkdb.InTx(api.Database, func(_ database.Store, ftx forkdb.Store) error {
		var err error
		pack, err = ftx.UpdatePluginPackCommit(ctx, forkdb.UpdatePluginPackCommitParams{
			ID:        pack.ID,
			Ref:       ref,
			CommitSha: snap.CommitSHA,
			UpdatedAt: dbtime.Now(),
		})
		if err != nil {
			return err
		}
		if err := ftx.DeletePluginPackResources(ctx, pack.ID); err != nil {
			return err
		}
		return insertForkPluginPackFiles(ctx, ftx, pack.ID, snap.Files)
	}, nil)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	api.writeForkPluginPack(ctx, rw, fdb, pack, http.StatusOK)
}

// @Summary Delete a plugin pack
// @ID delete-a-plugin-pack
// @Security CoderSessionToken
// @Tags Chats
// @Param organization path string true "Organization ID or name"
// @Param pluginPack path string true "Plugin pack ID" format(uuid)
// @Success 204
// @Router /api/v2/chats/fork/organizations/{organization}/plugin-packs/{pluginPack} [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkPluginPack(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fdb, pack, ok := api.forkPluginPackParam(rw, r)
	if !ok {
		return
	}
	if !api.authorizeForkPluginPacks(rw, r, policy.ActionDelete, pack) {
		return
	}
	if err := fdb.DeletePluginPackByID(ctx, pack.ID); err != nil {
		writeForkPluginPackError(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// authorizeForkPluginPacks answers 403 to a user who can read the object but
// not act on it, and 404 to a user who cannot read it either.
func (api *API) authorizeForkPluginPacks(rw http.ResponseWriter, r *http.Request, action policy.Action, object rbac.Objecter) bool {
	if api.Authorize(r, action, object) {
		return true
	}
	if api.Authorize(r, policy.ActionRead, object) {
		httpapi.Forbidden(rw)
	} else {
		httpapi.ResourceNotFound(rw)
	}
	return false
}

func forkPluginPackStore(rw http.ResponseWriter, db database.Store) (forkdb.Store, bool) {
	fdb, err := forkdb.From(db)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return nil, false
	}
	return fdb, true
}

// writeForkPluginPackError answers 404 for a missing row or a denied read.
// Handlers authorize writes first, so other errors are internal.
func writeForkPluginPackError(rw http.ResponseWriter, err error) {
	if httpapi.Is404Error(err) {
		httpapi.ResourceNotFound(rw)
		return
	}
	httpapi.InternalServerError(rw, err)
}

// forkPluginPackParam reads the pack of the {pluginPack} parameter and
// answers 404 when it is not in the organization of the path.
func (api *API) forkPluginPackParam(rw http.ResponseWriter, r *http.Request) (forkdb.Store, forkdb.PluginPack, bool) {
	id, ok := httpmw.ParseUUIDParam(rw, r, "pluginPack")
	if !ok {
		return nil, forkdb.PluginPack{}, false
	}
	fdb, ok := forkPluginPackStore(rw, api.Database)
	if !ok {
		return nil, forkdb.PluginPack{}, false
	}
	pack, err := fdb.GetPluginPackByID(r.Context(), id)
	if err == nil && pack.OrganizationID != httpmw.OrganizationParam(r).ID {
		err = sql.ErrNoRows
	}
	if err != nil {
		writeForkPluginPackError(rw, err)
		return nil, forkdb.PluginPack{}, false
	}
	return fdb, pack, true
}

func (api *API) fetchForkPluginPack(ctx context.Context, rw http.ResponseWriter, fdb forkdb.Store, organizationID uuid.UUID, src pluginpacks.Source) (pluginpacks.Snapshot, bool) {
	hosts, err := forkPluginPackSourceHosts(ctx, fdb, organizationID)
	if err != nil {
		writeForkPluginPackError(rw, err)
		return pluginpacks.Snapshot{}, false
	}
	fetcher := pluginpacks.Fetcher{AllowedHosts: hosts}
	if api.HTTPClient != nil {
		fetcher.Transport = api.HTTPClient.Transport
	}
	fetchCtx, cancel := context.WithTimeout(ctx, forkPluginPackFetchTimeout)
	defer cancel()
	snap, err := fetcher.Fetch(fetchCtx, src)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Failed to fetch the plugin pack.",
			Detail:  err.Error(),
		})
		return pluginpacks.Snapshot{}, false
	}
	return snap, true
}

func (*API) writeForkPluginPack(ctx context.Context, rw http.ResponseWriter, fdb forkdb.Store, pack forkdb.PluginPack, status int) {
	resources, err := fdb.GetPluginPackResources(ctx, pack.ID)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, status, convertForkPluginPack(pack, resources))
}

// forkPluginPackSourceHosts returns the allowed hosts of an organization. An
// organization that never set them allows none.
func forkPluginPackSourceHosts(ctx context.Context, fdb forkdb.Store, organizationID uuid.UUID) ([]string, error) {
	row, err := fdb.GetPluginPackSources(ctx, organizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return row.Hosts, nil
}

func insertForkPluginPackFiles(ctx context.Context, ftx forkdb.Store, packID uuid.UUID, files []pluginpacks.File) error {
	arg := forkdb.InsertPluginPackResourcesParams{PackID: packID}
	for _, f := range files {
		sum := sha256.Sum256(f.Content)
		arg.Paths = append(arg.Paths, f.Path)
		arg.Contents = append(arg.Contents, f.Content)
		arg.ContentHashes = append(arg.ContentHashes, hex.EncodeToString(sum[:]))
	}
	return ftx.InsertPluginPackResources(ctx, arg)
}

func convertForkPluginPack(pack forkdb.PluginPack, resources []forkdb.PluginPackResource) codersdk.ForkPluginPack {
	out := codersdk.ForkPluginPack{
		ID:             pack.ID,
		OrganizationID: pack.OrganizationID,
		Name:           pack.Name,
		SourceURL:      pack.SourceUrl,
		SourcePath:     pack.SourcePath,
		Ref:            pack.Ref,
		CommitSHA:      pack.CommitSha,
		CreatedBy:      pack.CreatedBy,
		CreatedAt:      pack.CreatedAt,
		UpdatedAt:      pack.UpdatedAt,
	}
	for _, res := range resources {
		out.Files = append(out.Files, codersdk.ForkPluginPackFile{Path: res.Path, SizeBytes: len(res.Content), ContentHash: res.ContentHash})
	}
	return out
}
