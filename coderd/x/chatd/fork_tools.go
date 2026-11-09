package chatd

// This file is fork code: it is the only place fork features add chat tools to
// a turn. Upstream's turn_environment.go calls appendForkTools once, so fork
// features never edit that file and an upstream sync of it stays clean.

import (
	"context"

	"charm.land/fantasy"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// appendForkTools returns the turn tool list plus the tools of the fork
// features enabled for this deployment. A fork feature gates on its own
// experiment with Server.experiments and a codersdk.ExperimentFork* constant,
// the way chatd gates upstream experiments, so with no fork experiment enabled
// the list is exactly the upstream list.
func (p *Server) appendForkTools(_ context.Context, tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	if p.experiments.Enabled(codersdk.ExperimentForkChatTodos) {
		tools = append(tools, p.chatTodoTools(chat)...)
	}
	return tools
}
