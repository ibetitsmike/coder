package chatd

// This file is fork code: it holds test helpers that keep upstream's chatd
// tests on upstream behavior when fork experiments exist.

import (
	"slices"

	"github.com/coder/coder/v2/codersdk"
)

// upstreamExperimentsKnown returns the known experiments without the fork
// experiments, so upstream tests run with every upstream experiment on and
// every fork feature off.
func upstreamExperimentsKnown() codersdk.Experiments {
	return slices.DeleteFunc(slices.Clone(codersdk.ExperimentsKnown), func(e codersdk.Experiment) bool {
		return slices.Contains(codersdk.ForkExperiments, e)
	})
}
