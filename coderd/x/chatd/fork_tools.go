package chatd

// This file is fork code: it is the only place fork features add chat tools to
// a turn. Upstream's turn_environment.go calls appendForkTools once, so fork
// features never edit that file and an upstream sync of it stays clean.

import (
	"context"
	"slices"

	"charm.land/fantasy"
	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// forkToolsOption carries the per-turn values that only turn_environment.go
// can supply. The variadic keeps every existing caller valid.
type forkToolsOption struct {
	getWorkspaceConn func(context.Context) (workspacesdk.AgentConn, error)
	model            *chatprovider.Model
	prompt           *[]fantasy.Message
	pending          []fantasy.Message
}

// forkToolsWithWorkspaceConn hands the turn's workspace connection getter to
// the fork tools that read workspace files during a tool call.
func forkToolsWithWorkspaceConn(get func(context.Context) (workspacesdk.AgentConn, error)) forkToolsOption {
	return forkToolsOption{getWorkspaceConn: get}
}

// forkToolsWithModel hands the turn's model and prompt to the fork features
// that depend on the model or bound the prompt: view_image checks that the
// model takes images, and limitViewedImages rewrites *prompt. pending is the
// unanswered user tail that the turn appends to *prompt later, so its media
// counts toward the image budget.
func forkToolsWithModel(model chatprovider.Model, prompt *[]fantasy.Message, pending []fantasy.Message) forkToolsOption {
	return forkToolsOption{model: &model, prompt: prompt, pending: pending}
}

// appendForkTools returns the turn tool list plus the tools of the fork
// features enabled for this deployment. A fork feature gates on its own
// experiment with Server.experiments and a codersdk.ExperimentFork* constant,
// the way chatd gates upstream experiments, so with no fork experiment enabled
// the list is exactly the upstream list.
func (p *Server) appendForkTools(ctx context.Context, tools []fantasy.AgentTool, chat database.Chat, modelConfigID uuid.UUID, opts ...forkToolsOption) ([]fantasy.AgentTool, error) {
	var opt forkToolsOption
	for _, o := range opts {
		if o.getWorkspaceConn != nil {
			opt.getWorkspaceConn = o.getWorkspaceConn
		}
		if o.model != nil {
			opt.model = o.model
		}
		if o.prompt != nil {
			opt.prompt = o.prompt
			opt.pending = o.pending
		}
	}
	// The bound applies with the experiment off too, so a chat that viewed
	// images before the experiment was turned off stays bounded. A
	// caller-supplied tool owns the name while the experiment is off, and its
	// results are not viewed images.
	if opt.prompt != nil && !p.dynamicToolOwnsViewImage(chat) {
		*opt.prompt = limitViewedImages(*opt.prompt, opt.pending, opt.model)
	}
	// A workflow step child gets workflow_report before the dynamic tools,
	// so the server tool wins the name (fork-workflows).
	tools, err := p.appendWorkflowReportTool(tools, chat)
	if err != nil {
		return nil, err
	}
	tools = append(tools, p.forkWorkflowTools(ctx, chat, modelConfigID, opt.getWorkspaceConn)...)
	nested, err := p.nestedSubagentTools(ctx, chat, modelConfigID)
	if err != nil {
		return nil, err
	}
	tools = p.withForkReplaySafeSpawn(append(tools, nested...), chat)
	if p.nestedSubagentsEnabled() {
		tools = p.withForkArchiveAgent(tools, chat)
	}
	if p.experiments.Enabled(codersdk.ExperimentForkChatTodos) {
		tools = append(tools, p.chatTodoTools(chat)...)
		tools = append(tools, p.chatTranscriptTool(chat))
		tools = append(tools, p.searchChatsTool(chat))
		tools = p.withForkWaitAgent(tools, chat)
	}
	if p.experiments.Enabled(codersdk.ExperimentForkChatWakeups) {
		tools = append(tools, p.scheduleWakeupTool(chat))
		tools = append(tools, p.waitForEventsTool(chat))
		tools = append(tools, p.notifyUserTool(chat))
	}
	if p.descendantQuestionsEnabled(chat) && !slices.ContainsFunc(tools, func(tool fantasy.AgentTool) bool {
		return tool.Info().Name == askUserQuestionToolName
	}) {
		tools = append(tools, p.newDescendantQuestionTool(chat))
	}
	// turn_environment.go gives the async question only to UI roots. An API
	// root gets it here: its owner can answer in the web UI, and the turn never
	// waits for the answer.
	if p.nestedSubagentsEnabled() && !chat.ParentChatID.Valid && !isExploreSubagentMode(chat.Mode) && !slices.ContainsFunc(tools, func(tool fantasy.AgentTool) bool {
		return tool.Info().Name == askUserQuestionToolName
	}) {
		tools = append(tools, chattool.NewAskUserQuestionTool(chattool.AskUserQuestionOptions{Async: true}))
	}
	if p.nestedSubagentsEnabled() {
		tools = p.withDescendantQuestionWait(tools, chat)
		tools = p.withForkListDescendants(tools, chat)
	}
	tools = p.withStoredSubagentTypes(p.withComputerUseSpawnOverrides(tools), chat)
	if p.experiments.Enabled(codersdk.ExperimentForkFamilyMessages) {
		tools = append(tools, p.familyMessageTools(chat)...)
	}
	if p.experiments.Enabled(codersdk.ExperimentForkViewImage) && opt.model != nil {
		tools = append(tools, viewImageTool(*opt.model, opt.getWorkspaceConn))
	}
	if p.experiments.Enabled(codersdk.ExperimentForkSubagentIsolation) {
		tools = p.withForkSpawnIsolation(tools, chat)
		tools = p.withForkIsolationReport(tools, chat)
		if tools, err = p.withForkWorktreeBinding(ctx, tools, chat); err != nil {
			return nil, err
		}
	}
	return tools, nil
}
