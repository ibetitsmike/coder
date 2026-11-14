package chatd

// This file is fork code: it ports the workflow step plumbing of the
// fork-workflows experiment from the upstream prototype coder/coder#31189
// (28f93181b6). A workflow step is a child chat whose ID the run derives
// deterministically from the run ID and the step spec, so a resumed run
// reuses the child chats of finished steps instead of running them again.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
)

// defaultStepSchema applies when a step has no schema.
const defaultStepSchema = `{"type":"object","properties":{"text":{"type":"string","description":"Your answer."}},"required":["text"]}`

// workflowStepPrompt goes into the system prompt of each step child.
const workflowStepPrompt = "You are one step of a workflow. When your work is done, call the " +
	workflowReportToolName + " tool one time with your result. The workflow reads only that value."

// workflowRunID derives the run ID from the tool call ID. Every replica
// and every retry of the call computes the same value.
func workflowRunID(callID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(callID, []byte("workflow-run"))
}

// workflowStepID gives the same child chat ID for the same spec in the
// same run. n counts earlier calls with the same spec, so a repeat is a
// new step.
func workflowStepID(runID uuid.UUID, specKey string, n int) uuid.UUID {
	return uuid.NewSHA1(runID, fmt.Appendf(nil, "%s#%d", specKey, n))
}

// stepSpec is the input of agent() and spawn(). The title is not part of
// the step key, so a new title reuses a finished step.
type stepSpec struct {
	Prompt string          `json:"prompt"`
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
	Title  string          `json:"-"`
}

func (s stepSpec) key() (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", xerrors.Errorf("marshal step spec: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// forkChildChatOverrides carries the fork fields that a workflow step needs
// on a child chat: its deterministic ID (applied by forkChildChatID before
// the prompt hooks dispatch), the run label, and the dynamic tools column
// with the workflow_report definition. A nil override keeps the upstream
// behavior exactly, so the hook is a no-op for every other spawn.
type forkChildChatOverrides struct {
	chatID       uuid.UUID
	labels       database.StringMap
	dynamicTools pqtype.NullRawMessage
}

// forkChildChatID returns the deterministic step chat ID for a workflow step
// spawn, the ID that a replay safe spawn_agent call derived from its tool
// call ID (fork_spawn_replay.go), or a fresh random ID for every other
// child. The ID is selected before the user prompt hooks dispatch: hook
// events carry the chat ID, and consumers correlate a chat's events by it, so
// the admission hook must see the ID the step chat is persisted with.
func (o childSubagentChatOptions) forkChildChatID(ctx context.Context) uuid.UUID {
	if o.fork != nil && o.fork.chatID != uuid.Nil {
		return o.fork.chatID
	}
	if id, ok := ctx.Value(forkSpawnChildIDKey{}).(uuid.UUID); ok && id != uuid.Nil {
		return id
	}
	return uuid.New()
}

// forkCreate is the one fork hook in createChildSubagentChatWithOptions.
// Without an override it calls createSubagentChatWithID as upstream does.
// The child's chat ID is already selected by forkChildChatID, so this hook
// applies only the label and dynamic tools overrides.
func (o childSubagentChatOptions) forkCreate(
	ctx context.Context,
	p *Server,
	publisher chatstate.Publisher,
	chatID uuid.UUID,
	parent database.Chat,
	input chatstate.CreateChatInput,
) (chatstate.CreateChatResult, error) {
	if o.fork == nil {
		return p.createSubagentChatWithID(ctx, publisher, chatID, parent, o.customAgent, input)
	}
	if o.fork.labels != nil {
		raw, err := json.Marshal(o.fork.labels)
		if err != nil {
			return chatstate.CreateChatResult{}, xerrors.Errorf("marshal fork child labels: %w", err)
		}
		input.Labels = pqtype.NullRawMessage{RawMessage: raw, Valid: true}
	}
	if o.fork.dynamicTools.Valid {
		input.DynamicTools = o.fork.dynamicTools
	}
	return p.createSubagentChatWithID(ctx, publisher, chatID, parent, o.customAgent, input)
}

// createWorkflowStepChat reuses the child chat of a step that an earlier
// runner created, or it creates the child chat. stepChatID comes from
// workflowStepID, so a resumed run computes the same ID for the same spec.
// reused is true when the child chat already existed.
func createWorkflowStepChat(
	ctx context.Context,
	p *Server,
	parent database.Chat,
	modelConfigID uuid.UUID,
	runID uuid.UUID,
	stepChatID uuid.UUID,
	spec stepSpec,
) (child database.Chat, reused bool, err error) {
	switch spec.Type {
	case "":
		spec.Type = subagentTypeGeneral
	case subagentTypeGeneral, subagentTypeExplore:
	default:
		return database.Chat{}, false, xerrors.Errorf("type must be %q or %q", subagentTypeGeneral, subagentTypeExplore)
	}
	if len(spec.Schema) == 0 {
		spec.Schema = json.RawMessage(defaultStepSchema)
	}
	// Validate the exact tool the turn loop builds for this child, not just
	// the raw schema: a schema that resolves but cannot inline into the
	// provider contract would otherwise persist an unusable child every
	// turn preparation then fails to build the tool for.
	if _, err := newWorkflowReportTool(spec.Schema); err != nil {
		return database.Chat{}, false, err
	}

	reused = true
	child, err = p.db.GetChatByID(ctx, stepChatID)
	if errors.Is(err, sql.ErrNoRows) {
		reused = false
		child, err = p.createStepChat(ctx, parent, modelConfigID, runID, stepChatID, spec)
		if database.IsUniqueViolation(err, database.UniqueChatsPkey) {
			reused = true
			child, err = p.db.GetChatByID(ctx, stepChatID) // another runner created it
		}
	}
	if err != nil {
		return database.Chat{}, false, xerrors.Errorf("start step %q: %w", spec.Title, err)
	}
	if child.ParentChatID.UUID != parent.ID {
		return database.Chat{}, false, xerrors.Errorf("step chat %s belongs to another chat", stepChatID)
	}
	return child, reused, nil
}

// createStepChat builds and creates one step child chat. The chat gets only
// the workflow_report dynamic tool, the step system prompt, and the run label
// that marks it as a step of this run.
func (p *Server) createStepChat(
	ctx context.Context,
	parent database.Chat,
	modelConfigID uuid.UUID,
	runID uuid.UUID,
	stepChatID uuid.UUID,
	spec stepSpec,
) (database.Chat, error) {
	parent, err := p.loadSubagentSpawnParentChat(ctx, func() database.Chat { return parent })
	if err != nil {
		return database.Chat{}, err
	}
	def, err := resolveSubagentDefinition(ctx, p, parent, spec.Type)
	if err != nil {
		return database.Chat{}, err
	}
	options, err := def.buildOptions(ctx, p, parent, parent, modelConfigID, nil, spec.Prompt)
	if err != nil {
		return database.Chat{}, err
	}
	tools, err := workflowStepTools(spec.Schema)
	if err != nil {
		return database.Chat{}, err
	}
	options.fork = &forkChildChatOverrides{
		chatID: stepChatID,
		labels: database.StringMap{workflowRunLabel: runID.String()},
		dynamicTools: pqtype.NullRawMessage{
			RawMessage: tools,
			Valid:      true,
		},
	}
	options.systemPrompt = strings.TrimSpace(options.systemPrompt + "\n\n" + workflowStepPrompt)
	// A step must reach workflow_report, so it never runs in plan mode.
	options.planModeOverride = &database.NullChatPlanMode{}
	return p.createChildSubagentChatWithOptions(ctx, parent, spec.Prompt, spec.Title, options)
}
