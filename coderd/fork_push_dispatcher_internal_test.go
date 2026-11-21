package coderd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/webpush"
	"github.com/coder/coder/v2/codersdk"
)

// With the experiment off or no key, chatd gets upstream's dispatcher
// itself, so web push behaves exactly as upstream's.
func TestForkChatPushDispatcherUnwrapped(t *testing.T) {
	t.Parallel()

	inner := &webpush.NoopWebpusher{}
	for _, tc := range []struct {
		name       string
		experiment bool
		key        bool
	}{
		{name: "Off"},
		{name: "NoKey", experiment: true},
		{name: "KeyWithoutExperiment", key: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := &codersdk.DeploymentValues{}
			if tc.key {
				require.NoError(t, values.AI.Chat.Fork.APNsKeyFile.Set("/missing/AuthKey.p8"))
			}
			api := &API{Options: &Options{DeploymentValues: values}}
			if tc.experiment {
				api.Experiments = codersdk.Experiments{codersdk.ExperimentForkAPNsPush}
			}
			require.Same(t, inner, api.forkChatPushDispatcher(inner))
			require.Nil(t, api.forkPush)
			api.closeForkPushDispatcher()
		})
	}
}

func TestForkChatPushKind(t *testing.T) {
	t.Parallel()

	require.Equal(t, forkPushFinishedTurn, forkPushKindOf("waiting"))
	require.Equal(t, forkPushFinishedTurn, forkPushKindOf("paused"))
	require.Equal(t, forkPushAgentError, forkPushKindOf("error"))
	require.Equal(t, forkPushQuestion, forkPushKindOf("running"))
	require.Equal(t, forkPushQuestion, forkPushKindOf("requires_action"))
}

// A key that does not load stops the server from starting.
func TestForkChatPushDispatcherBadKey(t *testing.T) {
	t.Parallel()

	values := &codersdk.DeploymentValues{}
	require.NoError(t, values.AI.Chat.Fork.APNsKeyFile.Set("/missing/AuthKey.p8"))
	api := &API{Options: &Options{DeploymentValues: values}, Experiments: codersdk.Experiments{codersdk.ExperimentForkAPNsPush}}
	require.PanicsWithError(t, "read APNs key file: open /missing/AuthKey.p8: no such file or directory", func() {
		api.forkChatPushDispatcher(&webpush.NoopWebpusher{})
	})
}
