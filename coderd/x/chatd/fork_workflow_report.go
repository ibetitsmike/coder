package chatd

// This file is fork code: it ports the workflow_report server tool of the
// fork-workflows experiment from the upstream prototype coder/coder#31189
// (28f93181b6). A workflow step child reports its result through this tool;
// chatd validates the value against the step schema and runs the tool itself,
// so the client never sees it.

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"strings"

	"charm.land/fantasy"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/codersdk"
)

const (
	// workflowReportToolName is the server tool that a workflow step child
	// calls with its result. The name is reserved: the dynamic tool parsers
	// skip it, so chatd runs it instead of the client.
	workflowReportToolName = "workflow_report"
	// workflowRunLabel marks a child chat as a workflow step. The value is
	// the run ID.
	workflowRunLabel = "coder_workflow_run"

	workflowReportDescription = "Report the result of your task to the workflow that started you. " +
		"Call this tool one time, when your work is done. The input must match the schema. " +
		"The workflow reads only this value. It does not read your final message."
	workflowReminder = "Call workflow_report now with a value that matches its schema. Do no other work."
)

// workflowReportTool validates its input against the step schema on the
// server. A valid value ends the turn through StopAfterTools. An invalid
// value returns an error result, and the model can try again in the same
// turn.
type workflowReportTool struct {
	info     fantasy.ToolInfo
	resolved *jsonschema.Resolved
	opts     fantasy.ProviderOptions
}

var _ fantasy.AgentTool = (*workflowReportTool)(nil)

func newWorkflowReportTool(raw json.RawMessage) (*workflowReportTool, error) {
	// The ref policy runs before the schema resolves: checkStepSchema hands
	// remote references to the JSON Schema resolver, whose error names a
	// missing loader instead of the unsupported reference.
	properties, required, err := stepReportParameters(raw)
	if err != nil {
		return nil, err
	}
	resolved, err := checkStepSchema(raw)
	if err != nil {
		return nil, err
	}
	return &workflowReportTool{
		info: fantasy.ToolInfo{
			Name:        workflowReportToolName,
			Description: workflowReportDescription,
			Parameters:  properties,
			Required:    required,
		},
		resolved: resolved,
	}, nil
}

func (t *workflowReportTool) Info() fantasy.ToolInfo { return t.info }

func (t *workflowReportTool) ProviderOptions() fantasy.ProviderOptions { return t.opts }

func (t *workflowReportTool) SetProviderOptions(opts fantasy.ProviderOptions) { t.opts = opts }

func (t *workflowReportTool) Run(_ context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var value any
	if err := json.Unmarshal([]byte(call.Input), &value); err != nil {
		return fantasy.NewTextErrorResponse("The input is not valid JSON: " + err.Error()), nil
	}
	// Validate the float64 decode against exactness first: jsonschema-go
	// cannot validate json.Number instances (they surface as strings), so
	// an integer beyond the float64 range would be compared rounded while
	// the workflow receives the original token below.
	if err := rejectOutOfRangeIntegers(call.Input); err != nil {
		return fantasy.NewTextErrorResponse(err.Error() +
			". Call workflow_report again with a corrected value."), nil
	}
	if err := t.resolved.Validate(value); err != nil {
		return fantasy.NewTextErrorResponse("The value does not match the schema: " + err.Error() +
			". Call workflow_report again with a corrected value."), nil
	}
	return fantasy.NewTextResponse("Value accepted. The workflow has your result."), nil
}

// maxExactInteger is the largest magnitude an integer report can carry.
// Reports are validated against the float64 decode of the raw input, so a
// bigger integer would be compared rounded (9007199254740993 decodes to
// 9007199254740992 and passes a maximum of 9007199254740992) while the
// workflow still receives the original token.
const maxExactInteger = 1 << 53

// rejectOutOfRangeIntegers fails when the input carries an integer literal
// outside the exact validation range. Floats beyond float64 range are
// rejected by the decode itself.
func rejectOutOfRangeIntegers(input string) error {
	bound := new(big.Int).Lsh(big.NewInt(1), 53)
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// The decode above already accepted the input.
			return nil
		}
		number, ok := token.(json.Number)
		if !ok {
			continue
		}
		text := number.String()
		if strings.ContainsAny(text, ".eE") {
			continue
		}
		integer, ok := new(big.Int).SetString(text, 10)
		if !ok {
			continue
		}
		if integer.Abs(integer).Cmp(bound) > 0 {
			return xerrors.Errorf("the report contains the integer %s, which exceeds the exact validation range (%d): bound the schema or send the value as a string", text, maxExactInteger)
		}
	}
}

// checkStepSchema accepts a JSON Schema whose top level is an object. The
// $schema keyword must be empty, draft-07 or draft 2020-12, because the
// validator supports only those.
func checkStepSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, xerrors.Errorf("parse step schema: %w", err)
	}
	if s.Type != "object" {
		return nil, xerrors.New(`the step schema must have "type": "object" at the top level`)
	}
	switch strings.TrimSpace(s.Schema) {
	case "",
		"http://json-schema.org/draft-07/schema#",
		"https://json-schema.org/draft-07/schema#",
		"https://json-schema.org/draft/2020-12/schema":
	default:
		return nil, xerrors.Errorf("unsupported $schema %q: use draft-07 or 2020-12", s.Schema)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		return nil, xerrors.Errorf("resolve step schema: %w", err)
	}
	return resolved, nil
}

// stepReportParameters extracts the fantasy ToolInfo parameter shape of a
// step schema. Fantasy rebuilds a tool's provider schema as
// {"type":"object","properties":Parameters,"required":Required}, which
// cannot carry the document's top-level $defs, so local $refs are inlined
// first and the properties are read from the inlined copy. Run still
// validates against the schema checkStepSchema resolved from the original,
// refs intact. A schema whose refs cannot inline faithfully is refused, so
// a broken shape is never shipped to the provider.
func stepReportParameters(raw json.RawMessage) (properties map[string]any, required []string, err error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil, xerrors.Errorf("parse report schema: %w", err)
	}
	inlined, err := inlineStepRefs(root, root, nil, new(int))
	if err != nil {
		return nil, nil, err
	}
	// Fantasy rebuilds the provider schema as
	// {type: object, properties: ..., required: ...}, so top-level
	// composition would silently vanish from the tool contract the child
	// sees, while Run still enforces it. Refuse instead of shipping a
	// weaker contract.
	for _, keyword := range []string{"allOf", "oneOf", "anyOf"} {
		if branches, ok := inlined[keyword].([]any); ok && len(branches) > 0 {
			return nil, nil, xerrors.Errorf(
				"top-level %s cannot be represented in the report tool contract: put the object constraints directly in properties", keyword)
		}
	}
	// A lone property named "*" is Fantasy's free-form wildcard spelling:
	// normalizeToolMapSchemas would read the parameters as
	// additionalProperties and the provider would accept keys the schema
	// forbids. Reject the name; escaping is impossible in JSON Schema.
	if props, ok := inlined["properties"].(map[string]any); ok {
		if _, star := props["*"]; star && len(props) == 1 {
			return nil, nil, xerrors.Errorf(
				`property name "*" is reserved by the tool contract: rename the property`)
		}
	}
	props, _ := inlined["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	if reqs, ok := inlined["required"].([]any); ok {
		for _, r := range reqs {
			if name, ok := r.(string); ok {
				required = append(required, name)
			}
		}
	}
	return props, required, nil
}

// inlineStepRefs returns a copy of node with every local $ref replaced by an
// inlined copy of the schema it points at. seen holds the refs on the chain
// currently being inlined, so recursive references are refused instead of
// looping. Remote references and $ref nodes with sibling keywords cannot be
// inlined faithfully and are refused as well.
// budget counts the maps the inlining materializes; diamond references
// (every definition referring to the previous one twice) grow the copy
// exponentially even though they are acyclic, so the budget bounds the
// work an O(n) schema can force.
func inlineStepRefs(node map[string]any, root map[string]any, seen []string, budget *int) (map[string]any, error) {
	if ref, ok := node["$ref"].(string); ok {
		if len(node) > 1 {
			return nil, xerrors.Errorf("$ref %q cannot be combined with sibling keywords: move them into the referenced schema", ref)
		}
		if !strings.HasPrefix(ref, "#/") {
			return nil, xerrors.Errorf("$ref %q is not a local reference: step schemas support only #/... references into the schema document", ref)
		}
		for _, s := range seen {
			if s == ref {
				return nil, xerrors.Errorf("$ref %q is recursive: step schemas cannot inline recursive references", ref)
			}
		}
		target, err := resolveSchemaPointer(root, ref)
		if err != nil {
			return nil, err
		}
		chain := make([]string, len(seen)+1)
		copy(chain, seen)
		chain[len(seen)] = ref
		return inlineStepRefs(target, root, chain, budget)
	}
	*budget++
	if *budget > maxInlinedNodes {
		return nil, xerrors.Errorf("step schema inlines to more than %d schema nodes: shrink or share the definitions", maxInlinedNodes)
	}
	out := make(map[string]any, len(node))
	for key, value := range node {
		inlined, err := inlineStepValue(key, value, root, seen, budget)
		if err != nil {
			return nil, err
		}
		out[key] = inlined
	}
	return out, nil
}

// maxInlinedNodes bounds how many schema maps one step schema may expand to.
// It is generous: real report schemas are a few dozen nodes, and the provider
// payload would be unreadable long before this bound matters.
const maxInlinedNodes = 4096

// schemaDataFields are keywords whose values are data, not schemas: an
// object or array under them is the asserted value itself, so a $ref-shaped
// field inside is literal JSON and must be copied verbatim instead of
// resolved or inlined.
var schemaDataFields = map[string]bool{
	"const":    true,
	"default":  true,
	"enum":     true,
	"examples": true,
}

// schemaNameMapFields are keywords whose value maps names to schemas, so
// their keys are property or definition names, never schema keywords.
var schemaNameMapFields = map[string]bool{
	"$defs":             true,
	"definitions":       true,
	"dependentSchemas":  true,
	"patternProperties": true,
	"properties":        true,
}

// inlineStepValue walks one keyword's value. Values under schemaDataFields
// are data and are copied verbatim. Maps under schemaNameMapFields are
// name-to-schema maps; every other map is a schema node, and array
// elements are schemas where they are objects.
func inlineStepValue(key string, value any, root map[string]any, seen []string, budget *int) (any, error) {
	if schemaDataFields[key] {
		return value, nil
	}
	switch typed := value.(type) {
	case map[string]any:
		if schemaNameMapFields[key] {
			out := make(map[string]any, len(typed))
			for name, schema := range typed {
				sub, ok := schema.(map[string]any)
				if !ok {
					out[name] = schema
					continue
				}
				inlined, err := inlineStepRefs(sub, root, seen, budget)
				if err != nil {
					return nil, err
				}
				out[name] = inlined
			}
			return out, nil
		}
		return inlineStepRefs(typed, root, seen, budget)
	case []any:
		out := make([]any, len(typed))
		for i, element := range typed {
			sub, ok := element.(map[string]any)
			if !ok {
				out[i] = element
				continue
			}
			inlined, err := inlineStepRefs(sub, root, seen, budget)
			if err != nil {
				return nil, err
			}
			out[i] = inlined
		}
		return out, nil
	default:
		return value, nil
	}
}

// resolveSchemaPointer walks ref (like "#/$defs/result") through root.
func resolveSchemaPointer(root map[string]any, ref string) (map[string]any, error) {
	current := any(root)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		node, ok := current.(map[string]any)
		if !ok {
			return nil, xerrors.Errorf("$ref %q does not resolve in the step schema", ref)
		}
		next, ok := node[part]
		if !ok {
			return nil, xerrors.Errorf("$ref %q does not resolve in the step schema", ref)
		}
		current = next
	}
	target, ok := current.(map[string]any)
	if !ok {
		return nil, xerrors.Errorf("$ref %q does not point at a schema object", ref)
	}
	return target, nil
}

// workflowStepTools returns the dynamic tools column for a step child. It
// holds only the workflow_report definition with the step schema.
func workflowStepTools(schema json.RawMessage) (json.RawMessage, error) {
	raw, err := json.Marshal([]codersdk.DynamicTool{{
		Name:        workflowReportToolName,
		Description: workflowReportDescription,
		InputSchema: schema,
	}})
	if err != nil {
		return nil, xerrors.Errorf("marshal step tools: %w", err)
	}
	return raw, nil
}

// chatIsWorkflowStep reports whether chat is a child chat the fork created
// for a workflow run: chatd sets the run label when it creates a step chat,
// and no caller-supplied path can set a label on a child chat (the public
// API only labels root chats, and the spawn path hardcodes empty labels).
// Every workflow_report touchpoint (server tool injection, turn stop,
// dynamic tool parsing) gates on this, so caller-supplied dynamic tools
// named workflow_report keep their upstream client-side behavior on chats
// the fork did not create.
func chatIsWorkflowStep(chat database.Chat) bool {
	return chat.ParentChatID.Valid && chat.Labels[workflowRunLabel] != ""
}

// WorkflowRunLabel is the chat label chatd sets on workflow step children.
const WorkflowRunLabel = workflowRunLabel

// IsWorkflowStepChat reports whether chat is a workflow step child the fork
// created. The public API guards the label and plan-mode writes a step chat
// must not receive with it.
func IsWorkflowStepChat(chat database.Chat) bool {
	return chatIsWorkflowStep(chat)
}

// workflowStepDef returns the workflow_report definition of a workflow step
// child, or false when chat is not one.
func workflowStepDef(chat database.Chat) (codersdk.DynamicTool, bool) {
	if !chatIsWorkflowStep(chat) || !chat.DynamicTools.Valid {
		return codersdk.DynamicTool{}, false
	}
	var defs []codersdk.DynamicTool
	if err := json.Unmarshal(chat.DynamicTools.RawMessage, &defs); err != nil {
		// appendDynamicTools reports a broken column. A broken column is
		// not a workflow step.
		return codersdk.DynamicTool{}, false //nolint:nilerr // see above
	}
	for _, def := range defs {
		if def.Name == workflowReportToolName {
			return def, true
		}
	}
	return codersdk.DynamicTool{}, false
}

// appendWorkflowReportTool adds workflow_report to the tools of a workflow
// step child. Other chats keep their tools.
func appendWorkflowReportTool(tools []fantasy.AgentTool, chat database.Chat) ([]fantasy.AgentTool, error) {
	def, ok := workflowStepDef(chat)
	if !ok {
		return tools, nil
	}
	tool, err := newWorkflowReportTool(def.InputSchema)
	if err != nil {
		// startStep checked the schema before it created the chat.
		return nil, xerrors.Errorf("build workflow_report for chat %s: %w", chat.ID, err)
	}
	return append(tools, tool), nil
}

// forkStopAfterWorkflowReport adds workflow_report to stop when chat is a
// workflow step child, so a valid report call ends the child's turn. It
// reads the chat's dynamic tools column, the same place appendWorkflowReportTool
// builds the tool from, so the two agree on which chats are step children.
func forkStopAfterWorkflowReport(stop map[string]struct{}, chat database.Chat) map[string]struct{} {
	if _, ok := workflowStepDef(chat); !ok {
		return stop
	}
	out := make(map[string]struct{}, len(stop)+1)
	for name := range stop {
		out[name] = struct{}{}
	}
	out[workflowReportToolName] = struct{}{}
	return out
}

// workflowStepReport reads a step child's history. value is the input of
// the last workflow_report call with a successful result. reminded is true
// when the child already got the reminder. The initial user message (the
// step prompt) is excluded from the reminder scan: an author could write
// or a hook could rewrite it to the reminder text, and that must not
// consume the runtime's retry reminder.
func workflowStepReport(
	ctx context.Context, db database.Store, chatID uuid.UUID,
) (value json.RawMessage, reminded bool, err error) {
	msgs, err := db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: chatID})
	if err != nil {
		return nil, false, xerrors.Errorf("get step messages: %w", err)
	}
	calls := map[string]json.RawMessage{}
	firstUser := true
	for _, msg := range msgs {
		parts, err := chatprompt.ParseContent(msg)
		if err != nil {
			continue
		}
		for _, part := range parts {
			switch {
			case msg.Role == database.ChatMessageRoleUser &&
				part.Type == codersdk.ChatMessagePartTypeText &&
				strings.TrimSpace(part.Text) == workflowReminder:
				// The step prompt is the first user message and is
				// user-authored (and hook-rewritable), so only a later
				// message can be the runtime's retry reminder.
				if !firstUser {
					reminded = true
				}
			case part.Type == codersdk.ChatMessagePartTypeToolCall &&
				part.ToolName == workflowReportToolName:
				calls[part.ToolCallID] = part.Args
			case part.Type == codersdk.ChatMessagePartTypeToolResult:
				// A result consumes its call entry whatever it says: a
				// rejected report must not answer a later, different tool
				// call that happens to reuse the call ID.
				args, ok := calls[part.ToolCallID]
				delete(calls, part.ToolCallID)
				if ok && !part.IsError {
					value = args // a later accepted call wins
				}
			}
		}
		if msg.Role == database.ChatMessageRoleUser {
			firstUser = false
		}
	}
	return value, reminded, nil
}
