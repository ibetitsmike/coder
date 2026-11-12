//nolint:testpackage // These tests cover the unexported applyReasoningEffort.
package chatprovider

import (
	"testing"

	"charm.land/fantasy"
	fantasyopenai "charm.land/fantasy/providers/openai"
	fantasyopenaicompat "charm.land/fantasy/providers/openaicompat"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
)

func compatReasoningEffort(t *testing.T, providerName, modelID, effort string) fantasyopenai.ReasoningEffort {
	t.Helper()
	model := NewModel(&chattest.FakeModel{ProviderName: providerName, ModelName: modelID}, nil)
	got := applyReasoningEffort(model, fantasy.ProviderOptions{}, &effort)
	providerOptions, ok := got[fantasyopenaicompat.Name].(*fantasyopenaicompat.ProviderOptions)
	require.True(t, ok, "%T", got[fantasyopenaicompat.Name])
	require.NotNil(t, providerOptions.ReasoningEffort)
	return *providerOptions.ReasoningEffort
}

func TestApplyReasoningEffortOpenAICompatGLM(t *testing.T) {
	t.Parallel()

	t.Run("MapsToTemplateLevels", func(t *testing.T) {
		t.Parallel()

		want := map[string]fantasyopenai.ReasoningEffort{
			codersdk.ChatModelReasoningEffortNone:    fantasyopenai.ReasoningEffortLow,
			codersdk.ChatModelReasoningEffortMinimal: fantasyopenai.ReasoningEffortLow,
			codersdk.ChatModelReasoningEffortLow:     fantasyopenai.ReasoningEffortLow,
			codersdk.ChatModelReasoningEffortMedium:  fantasyopenai.ReasoningEffortHigh,
			codersdk.ChatModelReasoningEffortHigh:    fantasyopenai.ReasoningEffortHigh,
			codersdk.ChatModelReasoningEffortXHigh:   fantasyopenai.ReasoningEffortMax,
			codersdk.ChatModelReasoningEffortMax:     fantasyopenai.ReasoningEffortMax,
		}
		require.Len(t, want, len(codersdk.ChatModelReasoningEffortValues()), "every effort on the global scale needs a GLM mapping")
		for _, modelID := range []string{"GLM-5.3-Flash-oQ4e-mtp", "glm-4.6", "zai-org/GLM-4.5-Air", " GLM-5 "} {
			for effort, wantEffort := range want {
				require.Equal(t, wantEffort, compatReasoningEffort(t, fantasyopenaicompat.Name, modelID, effort), "model %q effort %q", modelID, effort)
			}
		}
	})

	t.Run("OtherModelsKeepEffort", func(t *testing.T) {
		t.Parallel()

		for _, modelID := range []string{"qwen3-coder", "deepseek/deepseek-v4", "my-glm-model", "glm5", ""} {
			for _, effort := range codersdk.ChatModelReasoningEffortValues() {
				require.Equal(t, fantasyopenai.ReasoningEffort(effort), compatReasoningEffort(t, fantasyopenaicompat.Name, modelID, effort), "model %q effort %q", modelID, effort)
			}
		}
	})
}
