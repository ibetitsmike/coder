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
		require.Containsf(t, codersdk.ExperimentsKnown, experiment, "fork experiment %q must be in ExperimentsKnown", experiment)
	}

	// The append in init must add to the upstream list rather than replace it,
	// which is what happens if it runs before ExperimentsKnown initializes.
	require.Contains(t, codersdk.ExperimentsKnown, codersdk.ExperimentExample)
}
