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
	require.ErrorContains(t, values.Validate(), "--chat-max-subagent-depth (0) must be between 1 and 16")
	require.NoError(t, opt.Value.Set("17"))
	require.ErrorContains(t, values.Validate(), "--chat-max-subagent-depth (17) must be between 1 and 16")
	require.NoError(t, opt.Value.Set("3"))
	require.EqualValues(t, 3, values.AI.Chat.Fork.MaxSubagentDepth.Value())
	require.NoError(t, values.Validate())
}

func TestForkChatSubagentCapacityOptions(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{
		"chat-max-concurrent-subagents",
		"chat-max-concurrent-subagents-per-family",
		"chat-max-live-subagents-per-family",
	} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var values codersdk.DeploymentValues
			opts := values.Options()
			require.NoError(t, opts.SetDefaults())
			opt := opts.ByFlag(flag)
			require.NotNil(t, opt)
			require.Equal(t, "0", opt.Value.String(), "the default is no limit")
			require.NoError(t, values.Validate())

			require.NoError(t, opt.Value.Set("-1"))
			require.ErrorContains(t, values.Validate(), "--"+flag+" (-1) must be 0 or greater")
			require.NoError(t, opt.Value.Set("3"))
			require.NoError(t, values.Validate())
		})
	}
}

func TestForkChatAPNsOptions(t *testing.T) {
	t.Parallel()

	var values codersdk.DeploymentValues
	opts := values.Options()
	require.NoError(t, opts.SetDefaults())
	require.Equal(t, codersdk.DefaultChatAPNsTopic, values.AI.Chat.Fork.APNsTopic.Value())
	require.False(t, values.AI.Chat.Fork.APNsConfigured())
	require.NoError(t, values.Validate())

	// Validate covers YAML, which sets values without per-option validators.
	require.NoError(t, opts.ByFlag("chat-apns-key-file").Value.Set("/keys/AuthKey.p8"))
	require.ErrorContains(t, values.Validate(), "must be set together")
	require.NoError(t, opts.ByFlag("chat-apns-key-id").Value.Set("ABC123DEFG"))
	require.ErrorContains(t, values.Validate(), "must be set together")
	require.NoError(t, opts.ByFlag("chat-apns-team-id").Value.Set("TEAM123456"))
	require.NoError(t, values.Validate())
	require.True(t, values.AI.Chat.Fork.APNsConfigured())

	require.NoError(t, opts.ByFlag("chat-apns-topic").Value.Set(""))
	require.ErrorContains(t, values.Validate(), "--chat-apns-topic must not be empty")
}
