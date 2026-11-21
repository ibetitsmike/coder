package coderd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd/webpush"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
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
// A missing option or a key that does not load turns APNs off and leaves
// chatd with upstream's dispatcher, so a bad APNs config never stops the
// server.
func TestForkChatPushDispatcherBadConfig(t *testing.T) {
	t.Parallel()

	garbage := filepath.Join(t.TempDir(), "AuthKey.p8")
	require.NoError(t, os.WriteFile(garbage, []byte("SECRETGARBAGE"), 0o600))
	for _, tc := range []struct {
		name    string
		keyFile string
		keyID   string
		message string
		reason  string
	}{
		{name: "Unreadable", keyFile: filepath.Join(t.TempDir(), "missing.p8"), keyID: "TESTKEY123", message: "APNs push is off: the APNs key does not load", reason: "read APNs key file"},
		{name: "NotPEM", keyFile: garbage, keyID: "TESTKEY123", message: "APNs push is off: the APNs key does not load", reason: "APNs key file is not PEM"},
		{name: "MissingKeyID", keyFile: garbage, message: "APNs push is off: an APNs option is not set", reason: "--chat-apns-key-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := &codersdk.DeploymentValues{}
			require.NoError(t, values.AI.Chat.Fork.APNsKeyFile.Set(tc.keyFile))
			require.NoError(t, values.AI.Chat.Fork.APNsKeyID.Set(tc.keyID))
			require.NoError(t, values.AI.Chat.Fork.APNsTeamID.Set("TEAM123456"))
			require.NoError(t, values.AI.Chat.Fork.APNsTopic.Set(codersdk.DefaultChatAPNsTopic))
			sink := testutil.NewFakeSink(t)
			api := &API{
				ctx:         context.Background(),
				Options:     &Options{DeploymentValues: values, Logger: sink.Logger()},
				Experiments: codersdk.Experiments{codersdk.ExperimentForkAPNsPush},
			}
			inner := &webpush.NoopWebpusher{}
			require.Same(t, inner, api.forkChatPushDispatcher(inner))
			require.Nil(t, api.forkChatPushDispatcher(nil))
			require.Nil(t, api.forkPush)

			errors := sink.Entries(func(e slog.SinkEntry) bool { return e.Level == slog.LevelError })
			require.Len(t, errors, 2)
			require.Equal(t, tc.message, errors[0].Message)
			fields := fmt.Sprint(errors[0].Fields)
			require.Contains(t, fields, tc.reason)
			require.NotContains(t, fields, "SECRETGARBAGE")
		})
	}
}
