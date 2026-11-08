package codersdk

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// Plugin event names a plugin can request in its manifest.
const (
	PluginEventChatMessagesAdded = "chat.messages_added"
	PluginEventChatStatusChanged = "chat.status_changed"
)

// Plugin chat capabilities a plugin can request in its manifest. A plugin
// always acts as a linked user, so these never exceed what that user can do.
const (
	PluginChatCreate = "create"
	PluginChatSend   = "send"
	PluginChatRead   = "read"
)

// PluginManifest is what a plugin module requests. coderd reads it from the
// module in a sandbox before it stores the module.
type PluginManifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Events      []string `json:"events"`
	Chats       []string `json:"chats"`
	HTTPOrigins []string `json:"http_origins"`
	TickSeconds uint32   `json:"tick_seconds"`
	Webhook     bool     `json:"webhook"`
	ConfigKeys  []string `json:"config_keys"`
	// Connect means the plugin shows a prompt, such as a QR code, to users
	// who link an account in Settings > Linked accounts.
	Connect bool `json:"connect"`
}

// PluginGrants is what an owner granted a plugin. The capabilities must equal
// the manifest. PrivateCIDRs is the only grant a manifest cannot request.
type PluginGrants struct {
	Events      []string `json:"events"`
	Chats       []string `json:"chats"`
	HTTPOrigins []string `json:"http_origins"`
	TickSeconds uint32   `json:"tick_seconds"`
	Webhook     bool     `json:"webhook"`
	ConfigKeys  []string `json:"config_keys"`
	Connect     bool     `json:"connect"`
	// PrivateCIDRs lets HTTP requests of the plugin reach these private
	// address ranges, for example a signal-cli-rest-api in the cluster.
	PrivateCIDRs []string `json:"private_cidrs"`
}

// GrantsForManifest returns the grants that exactly cover a manifest.
func GrantsForManifest(m PluginManifest, privateCIDRs []string) PluginGrants {
	return PluginGrants{
		Events:       m.Events,
		Chats:        m.Chats,
		HTTPOrigins:  m.HTTPOrigins,
		TickSeconds:  m.TickSeconds,
		Webhook:      m.Webhook,
		ConfigKeys:   m.ConfigKeys,
		Connect:      m.Connect,
		PrivateCIDRs: privateCIDRs,
	}
}

// Plugin is an installed plugin. Config values are never returned.
type Plugin struct {
	ID             uuid.UUID      `json:"id" format:"uuid"`
	OrganizationID uuid.UUID      `json:"organization_id" format:"uuid"`
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	FileID         uuid.UUID      `json:"file_id" format:"uuid"`
	Manifest       PluginManifest `json:"manifest"`
	Grants         PluginGrants   `json:"grants"`
	// ConfigKeysSet lists the config keys that have a value.
	ConfigKeysSet []string   `json:"config_keys_set"`
	Enabled       bool       `json:"enabled"`
	RunnerID      *uuid.UUID `json:"runner_id,omitempty" format:"uuid"`
	LastError     string     `json:"last_error"`
	CreatedBy     uuid.UUID  `json:"created_by" format:"uuid"`
	CreatedAt     time.Time  `json:"created_at" format:"date-time"`
	UpdatedAt     time.Time  `json:"updated_at" format:"date-time"`
}

// InspectPluginResponse is the manifest of a module that is not installed.
type InspectPluginResponse struct {
	Manifest  PluginManifest `json:"manifest"`
	SHA256    string         `json:"sha256"`
	SizeBytes int            `json:"size_bytes"`
}

// CreatePluginRequest installs a plugin. Grants must equal the manifest of
// the module, or the request fails.
type CreatePluginRequest struct {
	Wasm   []byte            `json:"wasm"`
	Grants PluginGrants      `json:"grants"`
	Config map[string]string `json:"config,omitempty"`
}

// UpdatePluginRequest changes a plugin. A nil field does not change. A
// non-nil Config replaces all config values, so an empty map clears them.
type UpdatePluginRequest struct {
	Enabled *bool              `json:"enabled,omitempty"`
	Config  *map[string]string `json:"config,omitempty"`
}

// PluginLinkCode is a one-time code that links an external account, for
// example a Signal number, to the user that made it.
type PluginLinkCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at" format:"date-time"`
}

// PluginUserLink connects an external account to a Coder user.
type PluginUserLink struct {
	PluginID   uuid.UUID `json:"plugin_id" format:"uuid"`
	ExternalID string    `json:"external_id"`
	UserID     uuid.UUID `json:"user_id" format:"uuid"`
	CreatedAt  time.Time `json:"created_at" format:"date-time"`
}

// LinkablePlugin is a plugin the user can link an external account to.
type LinkablePlugin struct {
	ID               uuid.UUID `json:"id" format:"uuid"`
	OrganizationID   uuid.UUID `json:"organization_id" format:"uuid"`
	OrganizationName string    `json:"organization_name"`
	Name             string    `json:"name"`
	Version          string    `json:"version"`
	Chats            []string  `json:"chats"`
	// Connect means ConnectPlugin returns a prompt to show next to the link
	// code.
	Connect bool             `json:"connect"`
	Links   []PluginUserLink `json:"links"`
}

// PluginConnectPrompt is what a plugin shows a user who links an account,
// for example a QR code that adds Coder as a linked device of the user's
// Signal account.
type PluginConnectPrompt struct {
	// QRCodePNG is a PNG image of a QR code, omitted when the plugin shows
	// only instructions. JSON encodes it as base64.
	QRCodePNG    []byte `json:"qr_code_png,omitempty" swaggertype:"string" format:"byte"`
	Instructions string `json:"instructions"`
}

// InspectPlugin reads the manifest of a module without installing it.
func (c *ExperimentalClient) InspectPlugin(ctx context.Context, organizationID uuid.UUID, wasm []byte) (InspectPluginResponse, error) {
	res, err := c.Request(ctx, http.MethodPost, fmt.Sprintf("/api/experimental/organizations/%s/plugins/inspect", organizationID), wasm, func(r *http.Request) {
		r.Header.Set("Content-Type", "application/wasm")
	})
	if err != nil {
		return InspectPluginResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return InspectPluginResponse{}, ReadBodyAsError(res)
	}
	var resp InspectPluginResponse
	return resp, ReadBodyAsJSON(res, &resp)
}

// CreatePlugin installs a plugin in an organization.
func (c *ExperimentalClient) CreatePlugin(ctx context.Context, organizationID uuid.UUID, req CreatePluginRequest) (Plugin, error) {
	return requestJSON[Plugin](ctx, c.Client, http.MethodPost, fmt.Sprintf("/api/experimental/organizations/%s/plugins", organizationID), http.StatusCreated, req)
}

// Plugins lists the plugins of an organization.
func (c *ExperimentalClient) Plugins(ctx context.Context, organizationID uuid.UUID) ([]Plugin, error) {
	return requestJSON[[]Plugin](ctx, c.Client, http.MethodGet, fmt.Sprintf("/api/experimental/organizations/%s/plugins", organizationID), http.StatusOK, nil)
}

// Plugin returns one plugin.
func (c *ExperimentalClient) Plugin(ctx context.Context, pluginID uuid.UUID) (Plugin, error) {
	return requestJSON[Plugin](ctx, c.Client, http.MethodGet, fmt.Sprintf("/api/experimental/plugins/%s", pluginID), http.StatusOK, nil)
}

// UpdatePlugin changes the config of a plugin, or enables or disables it.
func (c *ExperimentalClient) UpdatePlugin(ctx context.Context, pluginID uuid.UUID, req UpdatePluginRequest) (Plugin, error) {
	return requestJSON[Plugin](ctx, c.Client, http.MethodPatch, fmt.Sprintf("/api/experimental/plugins/%s", pluginID), http.StatusOK, req)
}

// DeletePlugin removes a plugin, its links and its chat bindings.
func (c *ExperimentalClient) DeletePlugin(ctx context.Context, pluginID uuid.UUID) error {
	return requestNoContent(ctx, c.Client, http.MethodDelete, fmt.Sprintf("/api/experimental/plugins/%s", pluginID), nil)
}

// PluginLinks lists all links of a plugin.
func (c *ExperimentalClient) PluginLinks(ctx context.Context, pluginID uuid.UUID) ([]PluginUserLink, error) {
	return requestJSON[[]PluginUserLink](ctx, c.Client, http.MethodGet, fmt.Sprintf("/api/experimental/plugins/%s/links", pluginID), http.StatusOK, nil)
}

// LinkablePlugins lists the plugins the authenticated user can link to,
// with the links the user has.
func (c *ExperimentalClient) LinkablePlugins(ctx context.Context) ([]LinkablePlugin, error) {
	return requestJSON[[]LinkablePlugin](ctx, c.Client, http.MethodGet, "/api/experimental/users/me/plugins", http.StatusOK, nil)
}

// CreatePluginLinkCode makes a one-time link code for the authenticated
// user. A new code replaces the older codes of the user for the plugin.
func (c *ExperimentalClient) CreatePluginLinkCode(ctx context.Context, pluginID uuid.UUID) (PluginLinkCode, error) {
	return requestJSON[PluginLinkCode](ctx, c.Client, http.MethodPost, fmt.Sprintf("/api/experimental/plugins/%s/link-codes", pluginID), http.StatusCreated, nil)
}

// ConnectPlugin asks a plugin for the prompt that the authenticated user
// follows to connect an account. Each call can return a new prompt.
func (c *ExperimentalClient) ConnectPlugin(ctx context.Context, pluginID uuid.UUID) (PluginConnectPrompt, error) {
	return requestJSON[PluginConnectPrompt](ctx, c.Client, http.MethodPost, fmt.Sprintf("/api/experimental/plugins/%s/connect", pluginID), http.StatusOK, nil)
}

// DeletePluginLink removes a link. Users can remove their own links. Owners
// can remove any link.
func (c *ExperimentalClient) DeletePluginLink(ctx context.Context, pluginID uuid.UUID, externalID string) error {
	return requestNoContent(ctx, c.Client, http.MethodDelete, fmt.Sprintf("/api/experimental/plugins/%s/links/%s", pluginID, url.PathEscape(externalID)), nil)
}
