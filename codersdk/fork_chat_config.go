package codersdk

// This file is fork code: it holds the fork-only chat deployment options so
// upstream's ChatConfig and Options gain only one hook line each.

import (
	"strconv"

	"golang.org/x/xerrors"

	"github.com/coder/serpent"
)

// DefaultChatMaxSubagentDepth is the default maximum depth of nested subagent
// chats.
const DefaultChatMaxSubagentDepth = 2

// ForkChatConfig is the fork-only chat deployment configuration.
type ForkChatConfig struct {
	// MaxSubagentDepth is the maximum depth of nested subagent chats when
	// the fork-nested-subagents experiment is enabled. A root chat is at
	// depth 0.
	MaxSubagentDepth serpent.Int64 `json:"max_subagent_depth" typescript:",notnull"`
}

func (c *DeploymentValues) forkChatOptions(group *serpent.Group) serpent.OptionSet {
	return serpent.OptionSet{
		{
			Name:        "Chat: Max Subagent Depth",
			Description: "Maximum depth of nested subagent chats when the fork-nested-subagents experiment is enabled. A root chat is at depth 0 and its subagents at depth 1. Explore and computer use subagents never spawn subagents. Must be at least 1.",
			Flag:        "chat-max-subagent-depth",
			Env:         "CODER_CHAT_MAX_SUBAGENT_DEPTH",
			Value: serpent.Validate(&c.AI.Chat.Fork.MaxSubagentDepth, func(value *serpent.Int64) error {
				if value.Value() < 1 {
					return xerrors.New("must be at least 1")
				}
				return nil
			}),
			Default: strconv.Itoa(DefaultChatMaxSubagentDepth),
			Group:   group,
			YAML:    "maxSubagentDepth",
		},
	}
}
