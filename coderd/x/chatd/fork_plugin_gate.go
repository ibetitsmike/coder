package chatd

// This file is fork code: with the fork-plugins experiment off, resources
// contributed by a plugin pack must not reach the prompt or the context
// popup, so a deployment with the flag off matches upstream, whose resolver
// ignores plugin manifests. The agentcontext resolver stamps every
// pack-contributed resource with the pack's manifest path in source_path
// (agent/agentcontext/fork_plugins.go); this gate matches on that marker's
// base name. A user-added source path ending in plugin.json would be
// dropped too, which no current source type produces.

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// packContributionMarker is the base name of every plugin manifest the
// resolver stamps pack contributions with. Keep in sync with the manifest
// candidates in agent/agentcontext/fork_plugins.go.
const packContributionMarker = "plugin.json"

// isPackContribution reports whether a chat context resource row was
// contributed by a plugin pack.
func isPackContribution(sourcePath string) bool {
	return filepath.Base(strings.TrimSpace(sourcePath)) == packContributionMarker
}

// filterPackResources drops pack-contributed resources when fork-plugins is
// off. The prompt builder and the context resource listing both call it, so
// the flag gates every consumer of the pinned rows.
func (p *Server) filterPackResources(resources []database.ChatContextResource) []database.ChatContextResource {
	if p.experiments.Enabled(codersdk.ExperimentForkPlugins) {
		return resources
	}
	return slices.DeleteFunc(resources, func(r database.ChatContextResource) bool {
		return isPackContribution(r.SourcePath)
	})
}
