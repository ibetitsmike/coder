package codersdk

// This file is fork code: it is the client of the plugin pack routes of the
// fork-plugin-packs experiment.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ForkPluginPackSources are the Git hosts that plugin packs of an
// organization can be installed from.
type ForkPluginPackSources struct {
	Hosts []string `json:"hosts"`
}

// ForkPluginPack is a plugin pack installed from a Git repository and pinned
// to one commit.
type ForkPluginPack struct {
	ID             uuid.UUID `json:"id" format:"uuid"`
	OrganizationID uuid.UUID `json:"organization_id" format:"uuid"`
	Name           string    `json:"name"`
	SourceURL      string    `json:"source_url"`
	SourcePath     string    `json:"source_path"`
	Ref            string    `json:"ref"`
	CommitSHA      string    `json:"commit_sha"`
	CreatedBy      uuid.UUID `json:"created_by" format:"uuid"`
	CreatedAt      time.Time `json:"created_at" format:"date-time"`
	UpdatedAt      time.Time `json:"updated_at" format:"date-time"`
	// Files is set only when a single pack is returned.
	Files []ForkPluginPackFile `json:"files,omitempty"`
}

// ForkPluginPackFile is a stored file of a plugin pack.
type ForkPluginPackFile struct {
	Path        string `json:"path"`
	SizeBytes   int    `json:"size_bytes"`
	ContentHash string `json:"content_hash"`
}

// CreateForkPluginPackRequest installs a plugin pack.
type CreateForkPluginPackRequest struct {
	Name      string `json:"name"`
	SourceURL string `json:"source_url"`
	// SourcePath is the pack directory in the repository. Empty is the root.
	SourcePath string `json:"source_path,omitempty"`
	// Ref is a tag, a branch or a full commit SHA.
	Ref string `json:"ref"`
}

// UpdateForkPluginPackRequest fetches a plugin pack again.
type UpdateForkPluginPackRequest struct {
	// Ref replaces the ref of the pack. Empty fetches the current ref again.
	Ref string `json:"ref,omitempty"`
}

func forkPluginPacksPath(organizationID uuid.UUID) string {
	return fmt.Sprintf("/api/v2/chats/fork/organizations/%s/plugin-packs", organizationID)
}

func forkPluginPackSourcesPath(organizationID uuid.UUID) string {
	return fmt.Sprintf("/api/v2/chats/fork/organizations/%s/plugin-pack-sources", organizationID)
}

// ForkPluginPackSources returns the allowed plugin pack hosts of an
// organization.
func (c *Client) ForkPluginPackSources(ctx context.Context, organizationID uuid.UUID) (ForkPluginPackSources, error) {
	return requestJSON[ForkPluginPackSources](ctx, c, http.MethodGet, forkPluginPackSourcesPath(organizationID), http.StatusOK, nil)
}

// UpdateForkPluginPackSources replaces the allowed plugin pack hosts of an
// organization.
func (c *Client) UpdateForkPluginPackSources(ctx context.Context, organizationID uuid.UUID, req ForkPluginPackSources) (ForkPluginPackSources, error) {
	return requestJSON[ForkPluginPackSources](ctx, c, http.MethodPut, forkPluginPackSourcesPath(organizationID), http.StatusOK, req)
}

// ForkPluginPacks lists the plugin packs of an organization.
func (c *Client) ForkPluginPacks(ctx context.Context, organizationID uuid.UUID) ([]ForkPluginPack, error) {
	return requestJSON[[]ForkPluginPack](ctx, c, http.MethodGet, forkPluginPacksPath(organizationID), http.StatusOK, nil)
}

// ForkPluginPack returns a plugin pack with its files.
func (c *Client) ForkPluginPack(ctx context.Context, organizationID, packID uuid.UUID) (ForkPluginPack, error) {
	return requestJSON[ForkPluginPack](ctx, c, http.MethodGet, fmt.Sprintf("%s/%s", forkPluginPacksPath(organizationID), packID), http.StatusOK, nil)
}

// CreateForkPluginPack fetches a plugin pack and stores it pinned to the
// commit its ref resolves to.
func (c *Client) CreateForkPluginPack(ctx context.Context, organizationID uuid.UUID, req CreateForkPluginPackRequest) (ForkPluginPack, error) {
	return requestJSON[ForkPluginPack](ctx, c, http.MethodPost, forkPluginPacksPath(organizationID), http.StatusCreated, req)
}

// UpdateForkPluginPack fetches a plugin pack again and replaces its files.
func (c *Client) UpdateForkPluginPack(ctx context.Context, organizationID, packID uuid.UUID, req UpdateForkPluginPackRequest) (ForkPluginPack, error) {
	return requestJSON[ForkPluginPack](ctx, c, http.MethodPost, fmt.Sprintf("%s/%s/update", forkPluginPacksPath(organizationID), packID), http.StatusOK, req)
}

// DeleteForkPluginPack deletes a plugin pack and its files.
func (c *Client) DeleteForkPluginPack(ctx context.Context, organizationID, packID uuid.UUID) error {
	return requestNoContent(ctx, c, http.MethodDelete, fmt.Sprintf("%s/%s", forkPluginPacksPath(organizationID), packID), nil)
}
