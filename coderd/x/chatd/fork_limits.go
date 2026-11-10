package chatd

// This file is fork code: it holds the fork-only chat limits so upstream's
// Limits gains only one field.

import (
	"cmp"
	"database/sql"

	"github.com/coder/coder/v2/coderd/database/forkdb"
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
	// MaxLiveSubagentsPerFamily is the maximum number of live subagent
	// chats under one root chat.
	MaxLiveSubagentsPerFamily int
}

func forkLimitsFromConfig(cfg codersdk.ForkChatConfig) ForkLimits {
	return ForkLimits{
		MaxSubagentDepth:                int(cfg.MaxSubagentDepth.Value()),
		MaxConcurrentSubagents:          int(cfg.MaxConcurrentSubagents.Value()),
		MaxConcurrentSubagentsPerFamily: int(cfg.MaxConcurrentSubagentsPerFamily.Value()),
		MaxLiveSubagentsPerFamily:       int(cfg.MaxLiveSubagentsPerFamily.Value()),
	}
}

// WithDefaults sets the default subagent depth when it is unset.
func (l ForkLimits) WithDefaults() ForkLimits {
	l.MaxSubagentDepth = cmp.Or(l.MaxSubagentDepth, codersdk.DefaultChatMaxSubagentDepth)
	return l
}

// EffectiveSubagentLimits returns the subagent limits in force in an
// organization: the deployment limits, lowered by the organization's where
// it sets them. A capacity limit of 0 means no limit.
func EffectiveSubagentLimits(deployment ForkLimits, org forkdb.ChatOrganizationSubagentLimit) ForkLimits {
	limits := ForkLimits{
		MaxSubagentDepth:                deployment.MaxSubagentDepth,
		MaxConcurrentSubagents:          effectiveCapacityLimit(deployment.MaxConcurrentSubagents, org.MaxConcurrent),
		MaxConcurrentSubagentsPerFamily: effectiveCapacityLimit(deployment.MaxConcurrentSubagentsPerFamily, org.MaxConcurrentPerFamily),
		MaxLiveSubagentsPerFamily:       effectiveCapacityLimit(deployment.MaxLiveSubagentsPerFamily, org.MaxLivePerFamily),
	}
	if org.MaxDepth.Valid {
		limits.MaxSubagentDepth = min(limits.MaxSubagentDepth, int(org.MaxDepth.Int32))
	}
	return limits
}

// effectiveCapacityLimit lowers the deployment limit to the organization's.
// 0 means no limit.
func effectiveCapacityLimit(deployment int, org sql.NullInt32) int {
	if !org.Valid {
		return deployment
	}
	if deployment == 0 {
		return int(org.Int32)
	}
	return min(deployment, int(org.Int32))
}
