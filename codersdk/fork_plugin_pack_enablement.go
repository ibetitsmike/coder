package codersdk

// This file is fork code: it is the client of the routes that enable plugin
// packs for chat projects and chats in the fork-plugin-packs experiment.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// ForkEnabledPluginPack is a plugin pack enabled for a chat project or a chat.
// It leaves out the source and the files, which only organization admins can
// read.
type ForkEnabledPluginPack struct {
	ID        uuid.UUID `json:"id" format:"uuid"`
	Name      string    `json:"name"`
	CommitSHA string    `json:"commit_sha"`
}

func forkProjectPluginPacksPath(organizationID, projectID uuid.UUID) string {
	return fmt.Sprintf("/api/v2/chats/fork/organizations/%s/projects/%s/plugin-packs", organizationID, projectID)
}

func forkChatPluginPacksPath(chatID uuid.UUID) string {
	return fmt.Sprintf("/api/v2/chats/fork/%s/plugin-packs", chatID)
}

// ForkAvailablePluginPacks returns the plugin packs installed in an
// organization, for any member who may enable them.
func (c *Client) ForkAvailablePluginPacks(ctx context.Context, organizationID uuid.UUID) ([]ForkEnabledPluginPack, error) {
	return requestJSON[[]ForkEnabledPluginPack](ctx, c, http.MethodGet, fmt.Sprintf("/api/v2/chats/fork/organizations/%s/available-plugin-packs", organizationID), http.StatusOK, nil)
}

// ForkProjectPluginPacks returns the plugin packs enabled for a chat project.
func (c *Client) ForkProjectPluginPacks(ctx context.Context, organizationID, projectID uuid.UUID) ([]ForkEnabledPluginPack, error) {
	return requestJSON[[]ForkEnabledPluginPack](ctx, c, http.MethodGet, forkProjectPluginPacksPath(organizationID, projectID), http.StatusOK, nil)
}

// EnableForkProjectPluginPack enables a plugin pack for every chat of a chat
// project.
func (c *Client) EnableForkProjectPluginPack(ctx context.Context, organizationID, projectID, packID uuid.UUID) error {
	return requestNoContent(ctx, c, http.MethodPut, fmt.Sprintf("%s/%s", forkProjectPluginPacksPath(organizationID, projectID), packID), nil)
}

// DisableForkProjectPluginPack disables a plugin pack for a chat project.
func (c *Client) DisableForkProjectPluginPack(ctx context.Context, organizationID, projectID, packID uuid.UUID) error {
	return requestNoContent(ctx, c, http.MethodDelete, fmt.Sprintf("%s/%s", forkProjectPluginPacksPath(organizationID, projectID), packID), nil)
}

// ForkChatPluginPacks returns the plugin packs enabled for a chat itself, not
// those of its project.
func (c *Client) ForkChatPluginPacks(ctx context.Context, chatID uuid.UUID) ([]ForkEnabledPluginPack, error) {
	return requestJSON[[]ForkEnabledPluginPack](ctx, c, http.MethodGet, forkChatPluginPacksPath(chatID), http.StatusOK, nil)
}

// EnableForkChatPluginPack enables a plugin pack for a chat.
func (c *Client) EnableForkChatPluginPack(ctx context.Context, chatID, packID uuid.UUID) error {
	return requestNoContent(ctx, c, http.MethodPut, fmt.Sprintf("%s/%s", forkChatPluginPacksPath(chatID), packID), nil)
}

// DisableForkChatPluginPack disables a plugin pack for a chat.
func (c *Client) DisableForkChatPluginPack(ctx context.Context, chatID, packID uuid.UUID) error {
	return requestNoContent(ctx, c, http.MethodDelete, fmt.Sprintf("%s/%s", forkChatPluginPacksPath(chatID), packID), nil)
}
