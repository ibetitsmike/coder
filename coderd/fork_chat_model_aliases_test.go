package coderd_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkChatModelAliases(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client := newChatClient(t)
	org := coderdtest.CreateFirstUser(t, client.Client).OrganizationID
	openAI := createAIProviderForTest(t, client, "openai", "test-api-key")
	anthropic := createAIProviderForTest(t, client, "anthropic", "test-api-key")

	create := func(providerID uuid.UUID, model string, aliases ...string) (codersdk.ChatModel, error) {
		contextLimit := int64(4096)
		return client.CreateChatModel(ctx, org, codersdk.CreateChatModelRequest{
			AIProviderID: &providerID,
			Model:        model,
			ContextLimit: &contextLimit,
			ModelConfig:  &codersdk.ChatModelCallConfig{Aliases: aliases},
		})
	}
	requireInvalid := func(t *testing.T, err error, detail string) {
		t.Helper()
		sdkErr := requireSDKError(t, err, http.StatusBadRequest)
		require.Equal(t, "Invalid model aliases.", sdkErr.Message)
		require.Contains(t, sdkErr.Detail, detail)
	}

	mini, err := create(openAI.ID, "gpt-5-mini", "fast", "cheap")
	require.NoError(t, err)
	require.NotNil(t, mini.ModelConfig)
	require.Equal(t, []string{"fast", "cheap"}, mini.ModelConfig.Aliases)

	_, err = create(openAI.ID, "gpt-5-nano", "fast")
	requireInvalid(t, err, `alias "fast" already belongs to model "gpt-5-mini" of the same AI provider`)

	haiku, err := create(anthropic.ID, "claude-haiku-5", "fast")
	require.NoError(t, err, "another provider can reuse the alias")

	_, err = create(openAI.ID, "gpt-5", "inherit")
	requireInvalid(t, err, `alias "inherit" is reserved`)
	_, err = create(openAI.ID, "gpt-5", "Fast")
	requireInvalid(t, err, `alias "Fast" must be 1 to 32 lowercase letters, digits or hyphens`)
	_, err = create(openAI.ID, "gpt-5", "slow", "slow")
	requireInvalid(t, err, `alias "slow" is listed twice`)

	_, err = client.UpdateChatModel(ctx, org, mini.ID, codersdk.UpdateChatModelRequest{
		ModelConfig: &codersdk.ChatModelCallConfig{Aliases: []string{"fast"}},
	})
	require.NoError(t, err, "a config keeps its own alias")

	_, err = client.UpdateChatModel(ctx, org, haiku.ID, codersdk.UpdateChatModelRequest{
		AIProviderID: &openAI.ID,
		Model:        "gpt-5",
	})
	requireInvalid(t, err, `alias "fast" already belongs to model "gpt-5-mini"`)
}
