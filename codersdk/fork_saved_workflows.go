package codersdk

// This file is fork code: it holds the saved workflows of the fork-workflows
// experiment.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// ForkSavedWorkflowMetadata describes a saved Starlark workflow without its
// script.
type ForkSavedWorkflowMetadata struct {
	ID             uuid.UUID `json:"id" format:"uuid"`
	OrganizationID uuid.UUID `json:"organization_id" format:"uuid"`
	// UserID is the owner of a personal workflow. It is empty on an
	// organization workflow.
	UserID *uuid.UUID `json:"user_id,omitempty" format:"uuid"`
	// CreatedBy is the user who saved the workflow. It is empty when that
	// user was deleted.
	CreatedBy *uuid.UUID `json:"created_by,omitempty" format:"uuid"`
	// Name is what run_workflow takes to run the workflow.
	Name        string `json:"name"`
	Description string `json:"description"`
	// ArgsSchema is an optional JSON Schema object for the args of a run.
	ArgsSchema json.RawMessage `json:"args_schema,omitempty"`
	// Phases are the declared phase names of the workflow, in order.
	Phases    []string  `json:"phases"`
	CreatedAt time.Time `json:"created_at" format:"date-time"`
	UpdatedAt time.Time `json:"updated_at" format:"date-time"`
}

// ForkSavedWorkflow is a saved workflow with its script.
type ForkSavedWorkflow struct {
	ForkSavedWorkflowMetadata
	Script string `json:"script"`
}

// CreateForkSavedWorkflowRequest saves a workflow.
type CreateForkSavedWorkflowRequest struct {
	// Name is lowercase letters and digits in dash-separated words.
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Script is Starlark that defines main(args), as a run_workflow script.
	Script     string          `json:"script"`
	ArgsSchema json.RawMessage `json:"args_schema,omitempty"`
	Phases     []string        `json:"phases,omitempty"`
}

// UpdateForkSavedWorkflowRequest updates a saved workflow. At least one field
// must be set. An args_schema of null removes the schema, and an empty
// phases list removes the phases.
type UpdateForkSavedWorkflowRequest struct {
	Description *string         `json:"description,omitempty"`
	Script      *string         `json:"script,omitempty"`
	ArgsSchema  json.RawMessage `json:"args_schema,omitempty"`
	Phases      *[]string       `json:"phases,omitempty"`
}

func forkUserSavedWorkflowsPath(organizationID uuid.UUID, user string) string {
	return fmt.Sprintf("/api/v2/chats/fork/organizations/%s/members/%s/workflows", organizationID, url.PathEscape(user))
}

func forkUserSavedWorkflowPath(organizationID uuid.UUID, user, name string) string {
	return forkUserSavedWorkflowsPath(organizationID, user) + "/" + url.PathEscape(name)
}

// ForkUserSavedWorkflows lists the personal workflows of a user in an
// organization. user is a user ID, a username, or "me".
func (c *Client) ForkUserSavedWorkflows(ctx context.Context, organizationID uuid.UUID, user string) ([]ForkSavedWorkflowMetadata, error) {
	return requestJSON[[]ForkSavedWorkflowMetadata](ctx, c, http.MethodGet, forkUserSavedWorkflowsPath(organizationID, user), http.StatusOK, nil)
}

// ForkUserSavedWorkflow returns a personal workflow by name.
func (c *Client) ForkUserSavedWorkflow(ctx context.Context, organizationID uuid.UUID, user, name string) (ForkSavedWorkflow, error) {
	return requestJSON[ForkSavedWorkflow](ctx, c, http.MethodGet, forkUserSavedWorkflowPath(organizationID, user, name), http.StatusOK, nil)
}

// CreateForkUserSavedWorkflow saves a personal workflow for a user.
func (c *Client) CreateForkUserSavedWorkflow(ctx context.Context, organizationID uuid.UUID, user string, req CreateForkSavedWorkflowRequest) (ForkSavedWorkflow, error) {
	return requestJSON[ForkSavedWorkflow](ctx, c, http.MethodPost, forkUserSavedWorkflowsPath(organizationID, user), http.StatusCreated, req)
}

// UpdateForkUserSavedWorkflow updates a personal workflow.
func (c *Client) UpdateForkUserSavedWorkflow(ctx context.Context, organizationID uuid.UUID, user, name string, req UpdateForkSavedWorkflowRequest) (ForkSavedWorkflow, error) {
	return requestJSON[ForkSavedWorkflow](ctx, c, http.MethodPatch, forkUserSavedWorkflowPath(organizationID, user, name), http.StatusOK, req)
}

// DeleteForkUserSavedWorkflow deletes a personal workflow.
func (c *Client) DeleteForkUserSavedWorkflow(ctx context.Context, organizationID uuid.UUID, user, name string) error {
	return requestNoContent(ctx, c, http.MethodDelete, forkUserSavedWorkflowPath(organizationID, user, name), nil)
}

func forkOrganizationSavedWorkflowsPath(organizationID uuid.UUID) string {
	return fmt.Sprintf("/api/v2/chats/fork/organizations/%s/workflows", organizationID)
}

func forkOrganizationSavedWorkflowPath(organizationID uuid.UUID, name string) string {
	return forkOrganizationSavedWorkflowsPath(organizationID) + "/" + url.PathEscape(name)
}

// ForkOrganizationSavedWorkflows lists the organization workflows the caller
// can read.
func (c *Client) ForkOrganizationSavedWorkflows(ctx context.Context, organizationID uuid.UUID) ([]ForkSavedWorkflowMetadata, error) {
	return requestJSON[[]ForkSavedWorkflowMetadata](ctx, c, http.MethodGet, forkOrganizationSavedWorkflowsPath(organizationID), http.StatusOK, nil)
}

// ForkOrganizationSavedWorkflow returns an organization workflow by name.
func (c *Client) ForkOrganizationSavedWorkflow(ctx context.Context, organizationID uuid.UUID, name string) (ForkSavedWorkflow, error) {
	return requestJSON[ForkSavedWorkflow](ctx, c, http.MethodGet, forkOrganizationSavedWorkflowPath(organizationID, name), http.StatusOK, nil)
}

// CreateForkOrganizationSavedWorkflow saves a workflow for an organization.
// Every member can read it until its ACL is narrowed.
func (c *Client) CreateForkOrganizationSavedWorkflow(ctx context.Context, organizationID uuid.UUID, req CreateForkSavedWorkflowRequest) (ForkSavedWorkflow, error) {
	return requestJSON[ForkSavedWorkflow](ctx, c, http.MethodPost, forkOrganizationSavedWorkflowsPath(organizationID), http.StatusCreated, req)
}

// UpdateForkOrganizationSavedWorkflow updates an organization workflow.
func (c *Client) UpdateForkOrganizationSavedWorkflow(ctx context.Context, organizationID uuid.UUID, name string, req UpdateForkSavedWorkflowRequest) (ForkSavedWorkflow, error) {
	return requestJSON[ForkSavedWorkflow](ctx, c, http.MethodPatch, forkOrganizationSavedWorkflowPath(organizationID, name), http.StatusOK, req)
}

// DeleteForkOrganizationSavedWorkflow deletes an organization workflow.
func (c *Client) DeleteForkOrganizationSavedWorkflow(ctx context.Context, organizationID uuid.UUID, name string) error {
	return requestNoContent(ctx, c, http.MethodDelete, forkOrganizationSavedWorkflowPath(organizationID, name), nil)
}

// ForkOrganizationSavedWorkflowACL returns the users and groups that an
// organization workflow is shared with.
func (c *Client) ForkOrganizationSavedWorkflowACL(ctx context.Context, organizationID uuid.UUID, name string) (OrganizationSkillACL, error) {
	return requestJSON[OrganizationSkillACL](ctx, c, http.MethodGet, forkOrganizationSavedWorkflowPath(organizationID, name)+"/acl", http.StatusOK, nil)
}

// ForkOrganizationSavedWorkflowACLAvailable returns the organization members
// and groups that can be granted read access to an organization workflow.
// Pass UsersRequest{} when no filtering is desired.
func (c *Client) ForkOrganizationSavedWorkflowACLAvailable(ctx context.Context, organizationID uuid.UUID, name string, req UsersRequest) (ACLAvailable, error) {
	return requestJSON[ACLAvailable](ctx, c, http.MethodGet, forkOrganizationSavedWorkflowPath(organizationID, name)+"/acl/available", http.StatusOK, nil,
		req.Pagination.asRequestOption(),
		req.asRequestOption(),
	)
}

// UpdateForkOrganizationSavedWorkflowACL grants or revokes read access to an
// organization workflow for organization members and groups.
func (c *Client) UpdateForkOrganizationSavedWorkflowACL(ctx context.Context, organizationID uuid.UUID, name string, req UpdateOrganizationSkillACLRequest) error {
	return requestNoContent(ctx, c, http.MethodPatch, forkOrganizationSavedWorkflowPath(organizationID, name)+"/acl", req)
}
