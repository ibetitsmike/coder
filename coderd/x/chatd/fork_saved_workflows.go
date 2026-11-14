package chatd

// This file is fork code: it checks saved workflows of the fork-workflows
// experiment with the same rules run_workflow applies to a script, and it
// lets an agent find and run them. Agents only read saved workflows: users
// create, edit and delete them through the coderd routes.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"charm.land/fantasy"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

const (
	listWorkflowsToolName = "list_workflows"

	savedWorkflowTierPersonal     = "personal"
	savedWorkflowTierOrganization = "organization"

	// maxListedWorkflowDescription bounds the description of one entry in
	// the list. list_workflows with a name returns the full text.
	maxListedWorkflowDescription = 300
)

const listWorkflowsDescription = `List the saved workflows that run_workflow can run by name. ` +
	`They are your personal workflows and the organization workflows shared with you. ` +
	`Without "name", it returns each workflow's name, tier (personal or organization), a short description, its phases and whether it has an args schema. ` +
	`With "name", it returns that workflow's full description, args schema and phases. ` +
	`This tool only reads. Users save and edit workflows in Coder; agents cannot.`

// ValidateWorkflowScript reports whether script compiles as a workflow:
// it fits the run_workflow size limit, parses, names only the host API and
// defines main(args).
func ValidateWorkflowScript(script string) error {
	if len(script) > maxScriptBytes {
		return xerrors.Errorf("the script is %d bytes; at most %d bytes are allowed", len(script), maxScriptBytes)
	}
	_, err := compileWorkflow(script)
	return err
}

// ValidateWorkflowArgsSchema reports whether raw is a JSON Schema that
// run_workflow can check args against. Its top level must be an object.
func ValidateWorkflowArgsSchema(raw json.RawMessage) error {
	if _, err := checkStepSchema(raw); err != nil {
		return xerrors.Errorf("args schema: %w", err)
	}
	return nil
}

type listWorkflowsArgs struct {
	Name string `json:"name,omitempty" description:"Return the details of this saved workflow."`
}

type listedWorkflow struct {
	Name          string          `json:"name"`
	Tier          string          `json:"tier"`
	Description   string          `json:"description"`
	Phases        []string        `json:"phases,omitempty"`
	HasArgsSchema bool            `json:"has_args_schema"`
	ArgsSchema    json.RawMessage `json:"args_schema,omitempty"`
}

// savedWorkflowStore returns the fork queries and a context that reads as
// the chat owner, so dbauthz applies organization membership and the
// organization workflows' ACLs.
func (p *Server) savedWorkflowStore(ctx context.Context, chat database.Chat) (forkdb.Store, context.Context, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, nil, xerrors.Errorf("saved workflows are unavailable: %w", err)
	}
	owner, err := p.chatOwnerSubject(ctx, chat.OwnerID)
	if err != nil {
		return nil, nil, xerrors.Errorf("saved workflows are unavailable: %w", err)
	}
	//nolint:gocritic // Saved workflow ACLs are evaluated against the chat owner.
	return fdb, dbauthz.As(ctx, owner), nil
}

// loadSavedWorkflow returns the chat owner's personal workflow with this
// name in the chat's organization, else the organization workflow that is
// shared with the owner.
func (p *Server) loadSavedWorkflow(ctx context.Context, chat database.Chat, name string) (listedWorkflow, string, error) {
	fdb, ownerCtx, err := p.savedWorkflowStore(ctx, chat)
	if err != nil {
		return listedWorkflow{}, "", err
	}
	row, err := fdb.GetUserSavedWorkflowByName(ownerCtx, forkdb.GetUserSavedWorkflowByNameParams{
		OrganizationID: chat.OrganizationID,
		UserID:         chat.OwnerID,
		Name:           name,
	})
	tier := savedWorkflowTierPersonal
	if isSavedWorkflowMissing(err) {
		tier = savedWorkflowTierOrganization
		row, err = fdb.GetOrganizationSavedWorkflowByName(ownerCtx, forkdb.GetOrganizationSavedWorkflowByNameParams{
			OrganizationID: chat.OrganizationID,
			Name:           name,
		})
	}
	if isSavedWorkflowMissing(err) {
		return listedWorkflow{}, "", xerrors.Errorf("no saved workflow is named %q; call list_workflows to see the saved workflows", name)
	}
	if err != nil {
		return listedWorkflow{}, "", xerrors.Errorf("load saved workflow %q: %w", name, err)
	}
	entry := listedWorkflow{
		Name:          row.Name,
		Tier:          tier,
		Description:   row.Description,
		Phases:        row.Phases,
		HasArgsSchema: hasArgsSchema(row.ArgsSchema.Valid, row.ArgsSchema.RawMessage),
	}
	if entry.HasArgsSchema {
		entry.ArgsSchema = row.ArgsSchema.RawMessage
	}
	return entry, row.Script, nil
}

// isSavedWorkflowMissing reports a row that does not exist or that the
// owner cannot read. Both look the same to the agent.
func isSavedWorkflowMissing(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || dbauthz.IsNotAuthorizedError(err)
}

func hasArgsSchema(valid bool, raw json.RawMessage) bool {
	return valid && len(raw) > 0 && strings.TrimSpace(string(raw)) != "null"
}

// listSavedWorkflows returns the workflows that run_workflow can run by
// name for this chat: the owner's personal ones, then the shared
// organization ones that no personal workflow hides.
func (p *Server) listSavedWorkflows(ctx context.Context, chat database.Chat) ([]listedWorkflow, error) {
	fdb, ownerCtx, err := p.savedWorkflowStore(ctx, chat)
	if err != nil {
		return nil, err
	}
	personal, err := fdb.ListUserSavedWorkflowMetadata(ownerCtx, forkdb.ListUserSavedWorkflowMetadataParams{
		OrganizationID: chat.OrganizationID,
		UserID:         chat.OwnerID,
	})
	if err != nil && !dbauthz.IsNotAuthorizedError(err) {
		return nil, xerrors.Errorf("list personal workflows: %w", err)
	}
	organization, err := fdb.ListOrganizationSavedWorkflowMetadata(ownerCtx, chat.OrganizationID)
	if err != nil && !dbauthz.IsNotAuthorizedError(err) {
		return nil, xerrors.Errorf("list organization workflows: %w", err)
	}
	out := make([]listedWorkflow, 0, len(personal)+len(organization))
	seen := map[string]bool{}
	for _, row := range personal {
		seen[row.Name] = true
		out = append(out, listedWorkflow{
			Name:          row.Name,
			Tier:          savedWorkflowTierPersonal,
			Description:   truncateWorkflowDescription(row.Description),
			Phases:        row.Phases,
			HasArgsSchema: hasArgsSchema(row.ArgsSchema.Valid, row.ArgsSchema.RawMessage),
		})
	}
	for _, row := range organization {
		if seen[row.Name] {
			continue
		}
		out = append(out, listedWorkflow{
			Name:          row.Name,
			Tier:          savedWorkflowTierOrganization,
			Description:   truncateWorkflowDescription(row.Description),
			Phases:        row.Phases,
			HasArgsSchema: hasArgsSchema(row.ArgsSchema.Valid, row.ArgsSchema.RawMessage),
		})
	}
	return out, nil
}

func truncateWorkflowDescription(s string) string {
	if len(s) <= maxListedWorkflowDescription {
		return s
	}
	cut := maxListedWorkflowDescription
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// validateSavedWorkflowArgs checks args against the workflow's args
// schema, with the rules a workflow_report value meets. Missing args
// validate as an empty object, which main(args) receives.
func validateSavedWorkflowArgs(saved listedWorkflow, args any) error {
	if !saved.HasArgsSchema {
		return nil
	}
	resolved, err := checkStepSchema(saved.ArgsSchema)
	if err != nil {
		return xerrors.Errorf("the args schema of workflow %q is not valid: %w", saved.Name, err)
	}
	raw := []byte("{}")
	if args != nil {
		raw, err = json.Marshal(args)
		if err != nil {
			return xerrors.Errorf("marshal args: %w", err)
		}
	}
	if err := rejectOutOfRangeNumbers(string(raw)); err != nil {
		return xerrors.Errorf("args: %w", err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return xerrors.Errorf("decode args: %w", err)
	}
	if err := resolved.Validate(value); err != nil {
		return xerrors.Errorf("args do not match the args schema of workflow %q: %w", saved.Name, err)
	}
	return nil
}

func (p *Server) listWorkflowsTool(chat database.Chat) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		listWorkflowsToolName,
		listWorkflowsDescription,
		func(ctx context.Context, args listWorkflowsArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			var out any
			if name := strings.TrimSpace(args.Name); name != "" {
				entry, _, err := p.loadSavedWorkflow(ctx, chat, name)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				out = entry
			} else {
				entries, err := p.listSavedWorkflows(ctx, chat)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				out = map[string]any{"workflows": entries}
			}
			data, err := json.Marshal(out)
			if err != nil {
				return fantasy.NewTextErrorResponse("marshal saved workflows: " + err.Error()), nil
			}
			return fantasy.NewTextResponse(string(data)), nil
		},
	)
}
