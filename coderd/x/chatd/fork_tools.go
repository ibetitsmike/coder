package chatd

// This file is fork code: it is the only place fork features add chat tools to
// a turn. Upstream's turn_environment.go calls appendForkTools once, so fork
// features never edit that file and an upstream sync of it stays clean.

import (
	"context"

	"charm.land/fantasy"
	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
)

// appendForkTools returns the turn tool list plus the tools of the fork
// features enabled for this deployment. A fork feature gates on its own
// experiment with Server.experiments and
// codersdk.Experiment("fork-<feature>"), the way chatd gates upstream
// experiments, so with no fork experiment enabled the list is exactly the
// upstream list.
func (p *Server) appendForkTools(ctx context.Context, tools []fantasy.AgentTool, chat database.Chat, modelConfigID uuid.UUID) ([]fantasy.AgentTool, error) {
	nested, err := p.nestedSubagentTools(ctx, chat, modelConfigID)
	if err != nil {
		return nil, err
	}
	return append(tools, nested...), nil
}
