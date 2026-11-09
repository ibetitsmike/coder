package chatd

// This file is fork code: it holds the fork-only chat limits so upstream's
// Limits gains only one field.

import (
	"cmp"

	"github.com/coder/coder/v2/codersdk"
)

// ForkLimits are the fork deployment chat limits. A capacity limit of 0
// means no deployment limit.
type ForkLimits struct {
	// MaxSubagentDepth is the maximum depth of nested subagent chats. A
	// root chat is at depth 0.
	MaxSubagentDepth int
	// MaxConcurrentSubagents is the maximum number of running subagent
	// chats in the deployment.
	MaxConcurrentSubagents int
	// MaxConcurrentSubagentsPerFamily is the maximum number of running
	// subagent chats under one root chat.
	MaxConcurrentSubagentsPerFamily int
}

func forkLimitsFromConfig(cfg codersdk.ForkChatConfig) ForkLimits {
	return ForkLimits{
		MaxSubagentDepth:                int(cfg.MaxSubagentDepth.Value()),
		MaxConcurrentSubagents:          int(cfg.MaxConcurrentSubagents.Value()),
		MaxConcurrentSubagentsPerFamily: int(cfg.MaxConcurrentSubagentsPerFamily.Value()),
	}
}

func (l ForkLimits) withDefaults() ForkLimits {
	l.MaxSubagentDepth = cmp.Or(l.MaxSubagentDepth, codersdk.DefaultChatMaxSubagentDepth)
	return l
}
