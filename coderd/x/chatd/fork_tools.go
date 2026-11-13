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
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// forkToolsOption carries the per-turn values that only turn_environment.go
// can supply. The variadic keeps every existing caller valid.
type forkToolsOption struct {
	getWorkspaceConn func(context.Context) (workspacesdk.AgentConn, error)
}

// forkToolsWithWorkspaceConn hands the turn's workspace connection getter to
// the fork tools that read workspace files during a tool call.
func forkToolsWithWorkspaceConn(get func(context.Context) (workspacesdk.AgentConn, error)) forkToolsOption {
	return forkToolsOption{getWorkspaceConn: get}
}

// appendForkTools returns the turn tool list plus the tools of the fork
// features enabled for this deployment. A fork feature gates on its own
// experiment with Server.experiments and a codersdk.ExperimentFork* constant,
// the way chatd gates upstream experiments, so with no fork experiment enabled
// the list is exactly the upstream list.
func (p *Server) appendForkTools(ctx context.Context, tools []fantasy.AgentTool, chat database.Chat, modelConfigID uuid.UUID, opts ...forkToolsOption) ([]fantasy.AgentTool, error) {
	var opt forkToolsOption
	if len(opts) > 0 {
		opt = opts[0]
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
	tools = append(tools, nested...)
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
	if p.experiments.Enabled(codersdk.ExperimentForkSubagentIsolation) {
		tools = p.withForkSpawnIsolation(tools, chat)
		tools = p.withForkIsolationReport(tools, chat)
		if tools, err = p.withForkWorktreeBinding(ctx, tools, chat); err != nil {
			return nil, err
		}
	}
	return tools, nil
}
