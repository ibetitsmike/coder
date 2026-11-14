package codersdk_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
)

// TestForkExperiments guards the fork experiment contract: fork names are
// prefixed, and the fork list reaches ExperimentsKnown without dropping the
// upstream entries.
func TestForkExperiments(t *testing.T) {
	t.Parallel()

	for _, experiment := range codersdk.ForkExperiments {
		require.Truef(t,
			strings.HasPrefix(string(experiment), "fork-"),
			"fork experiment %q must be named fork-<feature>",
			experiment,
		)
	}
	// TestExperimentDisplayNames relies on the fork experiments coming last.
	require.Equal(t, codersdk.ForkExperiments, codersdk.ExperimentsKnown[len(codersdk.ExperimentsKnown)-len(codersdk.ForkExperiments):])

	// The append in init must add to the upstream list rather than replace it,
	// which is what happens if it runs before ExperimentsKnown initializes.
	require.Contains(t, codersdk.ExperimentsKnown, codersdk.ExperimentExample)

	// Fork experiments use the title-cased fallback display name.
	require.Equal(t, "Fork Token Budget", codersdk.ExperimentForkTokenBudget.DisplayName())
}

func TestForkExperimentDisplayNames(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Fork HTML Attachments", codersdk.ExperimentForkHTMLAttachments.DisplayName())
	// A fork experiment without its own name keeps the title-cased default.
	require.Equal(t, "Fork Chat Todos", codersdk.ExperimentForkChatTodos.DisplayName())
}
