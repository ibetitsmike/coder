package codersdk

// This file is fork code: it lists the agent types of the
// fork-agent-definitions experiment.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/google/uuid"
)

// ForkAgentTypeSourceBuiltin is the source of the built-in agent types. The
// sources of agent definitions are personal, org, project and workspace.
const ForkAgentTypeSourceBuiltin = "builtin"

// ForkAgentType is an agent type that spawn_agent offers a chat.
type ForkAgentType struct {
	// Name is the type that spawn_agent and CreateChatRequest.AgentType
	// accept: the bare name, or the source-qualified name, such as
	// personal/reviewer, when several sources define the name.
	Name string `json:"name"`
	// Source is builtin, personal, org, project or workspace.
	Source      string `json:"source"`
	Description string `json:"description"`
	// Model is the definition's model, or empty when the type runs on the
	// chat's model.
	Model string `json:"model,omitempty"`
	// BaseType is general or explore.
	BaseType string `json:"base_type"`
	// RootAvailable reports whether a new root chat can start as the type.
	RootAvailable bool `json:"root_available"`
	// RootUnavailableReason says why a new root chat cannot start as the
	// type.
	RootUnavailableReason string `json:"root_unavailable_reason,omitempty"`
}

// ForkAgentTypesRequest adds the agent definitions of a project or a
// workspace to ForkAgentTypes. A zero ID adds none.
type ForkAgentTypesRequest struct {
	ProjectID   uuid.UUID
	WorkspaceID uuid.UUID
}

// ForkAgentTypes lists the agent types that spawn_agent offers the caller's
// chats in the organization, with the request's project and workspace.
func (c *Client) ForkAgentTypes(ctx context.Context, organizationID uuid.UUID, req ForkAgentTypesRequest) ([]ForkAgentType, error) {
	query := url.Values{}
	if req.ProjectID != uuid.Nil {
		query.Set("project", req.ProjectID.String())
	}
	if req.WorkspaceID != uuid.Nil {
		query.Set("workspace", req.WorkspaceID.String())
	}
	path := fmt.Sprintf("/api/v2/chats/fork/organizations/%s/agent-types", organizationID)
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return requestJSON[[]ForkAgentType](ctx, c, http.MethodGet, path, http.StatusOK, nil)
}
