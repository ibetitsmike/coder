package coderd_test

// This file is fork code: it covers the /loop command errors of the
// fork-chat-wakeups experiment.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

// An invalid /loop command is the sender's mistake, so creating a chat or
// sending a message with one answers 400 with the reason.
func TestForkChatLoopErrors(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	values := coderdtest.DeploymentValues(t)
	values.Experiments = serpent.StringArray{string(codersdk.ExperimentForkChatWakeups)}
	client := newChatClientWithDeploymentValues(t, values)
	user := coderdtest.CreateFirstUser(t, client.Client)
	_ = createChatModel(t, client)
	text := func(s string) []codersdk.ChatInputPart {
		return []codersdk.ChatInputPart{{Type: codersdk.ChatInputPartTypeText, Text: s}}
	}
	requireInvalidLoop := func(t *testing.T, err error, reason string) {
		t.Helper()
		sdkErr := requireSDKError(t, err, http.StatusBadRequest)
		require.Equal(t, "Invalid /loop command.", sdkErr.Message)
		require.Contains(t, sdkErr.Detail, reason)
	}

	_, err := client.CreateChat(ctx, codersdk.CreateChatRequest{
		OrganizationID: user.OrganizationID,
		Content:        text("/loop 30s check CI"),
	})
	requireInvalidLoop(t, err, "interval must be between")

	chat, err := client.CreateChat(ctx, codersdk.CreateChatRequest{
		OrganizationID: user.OrganizationID,
		Content:        text("/loop 5m check CI"),
	})
	require.NoError(t, err)

	_, err = client.CreateChatMessage(ctx, chat.ID, codersdk.CreateChatMessageRequest{Content: text("/loop")})
	requireInvalidLoop(t, err, "usage: /loop [interval] <prompt>")

	_, err = client.CreateChatMessage(ctx, chat.ID, codersdk.CreateChatMessageRequest{Content: text("/loop 10m check CI")})
	require.NoError(t, err)
}
