package chatprovider

// This file is fork code: it maps the global reasoning effort scale for
// OpenAI-compatible models that upstream passes through unchanged. Upstream's
// reasoningeffort.go calls forkOpenAICompatReasoningEffort once, so an
// upstream sync of that file stays clean.

import (
	"strings"

	fantasyopenai "charm.land/fantasy/providers/openai"

	"github.com/coder/coder/v2/codersdk"
)

// forkOpenAICompatReasoningEffort returns the reasoning_effort to send for an
// OpenAI-compatible model, or current when no fork mapping applies.
func forkOpenAICompatReasoningEffort(modelID, effort string, current fantasyopenai.ReasoningEffort) fantasyopenai.ReasoningEffort {
	if mapped, ok := glmCompatReasoningEffort(modelID, effort); ok {
		return fantasyopenai.ReasoningEffort(mapped)
	}
	return current
}

// glmCompatReasoningEffort maps the global reasoning effort scale onto the
// reasoning_effort values a GLM chat template understands, reporting ok=false
// for non-GLM model IDs. The GLM template (as served by oMLX) renders a
// reasoning hint only for exactly "low" or "high" and runs every other value,
// including an absent field, at maximum thinking. Passing medium through
// therefore thinks harder than high, so each effort snaps to the nearest
// level the template distinguishes.
func glmCompatReasoningEffort(modelID, effort string) (string, bool) {
	if !isGLMModelID(modelID) {
		return "", false
	}
	switch effort {
	case codersdk.ChatModelReasoningEffortNone,
		codersdk.ChatModelReasoningEffortMinimal,
		codersdk.ChatModelReasoningEffortLow:
		return codersdk.ChatModelReasoningEffortLow, true
	case codersdk.ChatModelReasoningEffortMedium,
		codersdk.ChatModelReasoningEffortHigh:
		return codersdk.ChatModelReasoningEffortHigh, true
	default:
		return codersdk.ChatModelReasoningEffortMax, true
	}
}

// isGLMModelID reports whether modelID names a GLM model, ignoring case and
// any gateway or repository prefix such as "zai-org/".
func isGLMModelID(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	return strings.HasPrefix(id, "glm-")
}
