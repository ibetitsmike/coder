package chatd

// This file is fork code: it holds the fork-only chat limits so upstream's
// Limits gains only one field.

import (
	"cmp"

	"github.com/coder/coder/v2/codersdk"
)

// ForkLimits are the fork deployment chat limits.
type ForkLimits struct {
	// MaxSubagentDepth is the maximum depth of nested subagent chats. A
	// root chat is at depth 0.
	MaxSubagentDepth int
}

func forkLimitsFromConfig(cfg codersdk.ForkChatConfig) ForkLimits {
	return ForkLimits{
		MaxSubagentDepth: int(cfg.MaxSubagentDepth.Value()),
	}
}

func (l ForkLimits) withDefaults() ForkLimits {
	return ForkLimits{
		MaxSubagentDepth: cmp.Or(l.MaxSubagentDepth, codersdk.DefaultChatMaxSubagentDepth),
	}
}
