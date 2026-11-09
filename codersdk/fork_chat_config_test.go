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

	require.Error(t, opt.Value.Set("0"))
	require.NoError(t, opt.Value.Set("3"))
	require.EqualValues(t, 3, values.AI.Chat.Fork.MaxSubagentDepth.Value())
}
