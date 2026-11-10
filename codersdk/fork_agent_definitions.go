package codersdk

// This file is fork code: it holds the stored agent definitions of the
// fork-agent-definitions experiment.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// ChatContextResourceKindSubagent is a custom agent definition file found in
// the workspace.
const ChatContextResourceKindSubagent ChatContextResourceKind = "subagent"

// ForkAgentDefinitionMetadata describes a stored custom agent definition
// without its content.
type ForkAgentDefinitionMetadata struct {
	ID             uuid.UUID `json:"id" format:"uuid"`
	OrganizationID uuid.UUID `json:"organization_id" format:"uuid"`
	// Name is the spawn_agent type, taken from the front-matter of the
	// content.
	Name        string `json:"name"`
	Description string `json:"description"`
	// Enabled reports whether chats are offered the definition.
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at" format:"date-time"`
	UpdatedAt time.Time `json:"updated_at" format:"date-time"`
}

// ForkAgentDefinition is a stored custom agent definition with its raw
// markdown content.
type ForkAgentDefinition struct {
	ForkAgentDefinitionMetadata
	Content string `json:"content"`
}

// CreateForkAgentDefinitionRequest creates a stored agent definition.
type CreateForkAgentDefinitionRequest struct {
	// Content is a Claude Code or Cursor agent markdown file. Its
	// front-matter must include name and must be followed by a prompt body.
	Content string `json:"content"`
}

// UpdateForkAgentDefinitionRequest updates a stored agent definition. At
// least one field must be set, and new content must keep the definition's
// name.
type UpdateForkAgentDefinitionRequest struct {
	Content *string `json:"content,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

func forkUserAgentDefinitionsPath(organizationID uuid.UUID, user string) string {
	return fmt.Sprintf("/api/v2/chats/fork/organizations/%s/members/%s/agent-definitions", organizationID, url.PathEscape(user))
}

func forkUserAgentDefinitionPath(organizationID uuid.UUID, user, name string) string {
	return forkUserAgentDefinitionsPath(organizationID, user) + "/" + url.PathEscape(name)
}

// ForkUserAgentDefinitions lists the agent definitions of a user in an
// organization. user is a user ID, a username, or "me".
func (c *Client) ForkUserAgentDefinitions(ctx context.Context, organizationID uuid.UUID, user string) ([]ForkAgentDefinitionMetadata, error) {
	return requestJSON[[]ForkAgentDefinitionMetadata](ctx, c, http.MethodGet, forkUserAgentDefinitionsPath(organizationID, user), http.StatusOK, nil)
}

// ForkUserAgentDefinition returns a user's agent definition by name.
func (c *Client) ForkUserAgentDefinition(ctx context.Context, organizationID uuid.UUID, user, name string) (ForkAgentDefinition, error) {
	return requestJSON[ForkAgentDefinition](ctx, c, http.MethodGet, forkUserAgentDefinitionPath(organizationID, user, name), http.StatusOK, nil)
}

// CreateForkUserAgentDefinition stores an agent definition for a user.
func (c *Client) CreateForkUserAgentDefinition(ctx context.Context, organizationID uuid.UUID, user string, req CreateForkAgentDefinitionRequest) (ForkAgentDefinition, error) {
	return requestJSON[ForkAgentDefinition](ctx, c, http.MethodPost, forkUserAgentDefinitionsPath(organizationID, user), http.StatusCreated, req)
}

// UpdateForkUserAgentDefinition updates a user's agent definition.
func (c *Client) UpdateForkUserAgentDefinition(ctx context.Context, organizationID uuid.UUID, user, name string, req UpdateForkAgentDefinitionRequest) (ForkAgentDefinition, error) {
	return requestJSON[ForkAgentDefinition](ctx, c, http.MethodPatch, forkUserAgentDefinitionPath(organizationID, user, name), http.StatusOK, req)
}

// DeleteForkUserAgentDefinition deletes a user's agent definition.
func (c *Client) DeleteForkUserAgentDefinition(ctx context.Context, organizationID uuid.UUID, user, name string) error {
	return requestNoContent(ctx, c, http.MethodDelete, forkUserAgentDefinitionPath(organizationID, user, name), nil)
}

func forkOrganizationAgentDefinitionsPath(organizationID uuid.UUID) string {
	return fmt.Sprintf("/api/v2/chats/fork/organizations/%s/agent-definitions", organizationID)
}

func forkOrganizationAgentDefinitionPath(organizationID uuid.UUID, name string) string {
	return forkOrganizationAgentDefinitionsPath(organizationID) + "/" + url.PathEscape(name)
}

// ForkOrganizationAgentDefinitions lists the organization agent definitions
// the caller can read.
func (c *Client) ForkOrganizationAgentDefinitions(ctx context.Context, organizationID uuid.UUID) ([]ForkAgentDefinitionMetadata, error) {
	return requestJSON[[]ForkAgentDefinitionMetadata](ctx, c, http.MethodGet, forkOrganizationAgentDefinitionsPath(organizationID), http.StatusOK, nil)
}

// ForkOrganizationAgentDefinition returns an organization agent definition by
// name.
func (c *Client) ForkOrganizationAgentDefinition(ctx context.Context, organizationID uuid.UUID, name string) (ForkAgentDefinition, error) {
	return requestJSON[ForkAgentDefinition](ctx, c, http.MethodGet, forkOrganizationAgentDefinitionPath(organizationID, name), http.StatusOK, nil)
}

// CreateForkOrganizationAgentDefinition stores an agent definition for an
// organization. Every member can read it until its ACL is narrowed.
func (c *Client) CreateForkOrganizationAgentDefinition(ctx context.Context, organizationID uuid.UUID, req CreateForkAgentDefinitionRequest) (ForkAgentDefinition, error) {
	return requestJSON[ForkAgentDefinition](ctx, c, http.MethodPost, forkOrganizationAgentDefinitionsPath(organizationID), http.StatusCreated, req)
}

// UpdateForkOrganizationAgentDefinition updates an organization agent
// definition.
func (c *Client) UpdateForkOrganizationAgentDefinition(ctx context.Context, organizationID uuid.UUID, name string, req UpdateForkAgentDefinitionRequest) (ForkAgentDefinition, error) {
	return requestJSON[ForkAgentDefinition](ctx, c, http.MethodPatch, forkOrganizationAgentDefinitionPath(organizationID, name), http.StatusOK, req)
}

// DeleteForkOrganizationAgentDefinition deletes an organization agent
// definition.
func (c *Client) DeleteForkOrganizationAgentDefinition(ctx context.Context, organizationID uuid.UUID, name string) error {
	return requestNoContent(ctx, c, http.MethodDelete, forkOrganizationAgentDefinitionPath(organizationID, name), nil)
}

// ForkOrganizationAgentDefinitionACL returns the users and groups that an
// organization agent definition is shared with.
func (c *Client) ForkOrganizationAgentDefinitionACL(ctx context.Context, organizationID uuid.UUID, name string) (OrganizationSkillACL, error) {
	return requestJSON[OrganizationSkillACL](ctx, c, http.MethodGet, forkOrganizationAgentDefinitionPath(organizationID, name)+"/acl", http.StatusOK, nil)
}

// UpdateForkOrganizationAgentDefinitionACL grants or revokes read access to
// an organization agent definition for organization members and groups.
func (c *Client) UpdateForkOrganizationAgentDefinitionACL(ctx context.Context, organizationID uuid.UUID, name string, req UpdateOrganizationSkillACLRequest) error {
	return requestNoContent(ctx, c, http.MethodPatch, forkOrganizationAgentDefinitionPath(organizationID, name)+"/acl", req)
}
