package codersdk_test

// This file is fork code: it tests the fork-only chat deployment options.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
)

func TestForkChatMaxSubagentDepthOption(t *testing.T) {
	t.Parallel()

	var values codersdk.DeploymentValues
	opts := values.Options()
	opt := opts.ByFlag("chat-max-subagent-depth")
	require.NotNil(t, opt)
	require.Equal(t, "CODER_CHAT_MAX_SUBAGENT_DEPTH", opt.Env)
	require.NoError(t, opts.SetDefaults())
	require.EqualValues(t, codersdk.DefaultChatMaxSubagentDepth, values.AI.Chat.Fork.MaxSubagentDepth.Value())

	require.NoError(t, values.Validate())

	// Validate covers YAML, which sets values without per-option validators.
	require.NoError(t, opt.Value.Set("0"))
	require.ErrorContains(t, values.Validate(), "--chat-max-subagent-depth (0) must be between 1 and")
	require.NoError(t, opt.Value.Set("3"))
	require.EqualValues(t, 3, values.AI.Chat.Fork.MaxSubagentDepth.Value())
	require.NoError(t, values.Validate())
}
