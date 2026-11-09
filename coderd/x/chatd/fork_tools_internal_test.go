package chatd

import (
	"context"
	"slices"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// TestAppendForkTools pins the fork tool contract: with every fork experiment
// off the hook returns the turn tool list untouched, even when all upstream
// experiments are on, so fork code cannot change the tools an upstream
// deployment sees.
func TestAppendForkTools(t *testing.T) {
	t.Parallel()

	tools := []fantasy.AgentTool{
		forkToolsTestTool("read_file"),
		forkToolsTestTool("execute"),
		forkToolsTestTool("ask_user_question"),
	}
	ctx := context.Background()
	// The Server has no database, so a fork hook that reaches the store
	// for a delegated chat with its experiment off fails the test.
	chats := []database.Chat{{}, {ParentChatID: uuid.NullUUID{UUID: uuid.New(), Valid: true}}}
	upstream := slices.DeleteFunc(slices.Clone(codersdk.ExperimentsKnown), func(e codersdk.Experiment) bool {
		return slices.Contains(codersdk.ForkExperiments, e)
	})

	for _, experiments := range []codersdk.Experiments{{}, upstream} {
		for _, chat := range chats {
			p := &Server{experiments: experiments}
			got, err := p.appendForkTools(ctx, tools, chat, uuid.Nil)
			require.NoError(t, err)
			require.Len(t, got, len(tools), "tool count changed with experiments %v", experiments)
			for i, want := range tools {
				require.Samef(t, want, got[i], "tool %d changed with experiments %v", i, experiments)
			}
		}
	}
}

func forkToolsTestTool(name string) fantasy.AgentTool {
	return fantasy.NewAgentTool[struct{}](name, "test tool", func(
		_ context.Context,
		_ struct{},
		_ fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		return fantasy.ToolResponse{}, nil
	})
}
