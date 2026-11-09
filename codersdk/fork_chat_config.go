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

// MaxChatMaxSubagentDepth caps --chat-max-subagent-depth so walks over a
// subagent family stay small and bounded.
const MaxChatMaxSubagentDepth = 16

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
			Description: "Maximum depth of nested subagent chats when the fork-nested-subagents experiment is enabled. A root chat is at depth 0 and its subagents at depth 1. Explore and computer use subagents never spawn subagents. Must be between 1 and 16.",
			Flag:        "chat-max-subagent-depth",
			Env:         "CODER_CHAT_MAX_SUBAGENT_DEPTH",
			Value:       &c.AI.Chat.Fork.MaxSubagentDepth,
			Default:     strconv.Itoa(DefaultChatMaxSubagentDepth),
			Group:       group,
			YAML:        "maxSubagentDepth",
		},
	}
}

// validateForkChat runs from Validate rather than as a per-option validator,
// which only fires in Set and so misses the YAML path.
func (c *DeploymentValues) validateForkChat() error {
	if depth := c.AI.Chat.Fork.MaxSubagentDepth.Value(); depth < 1 || depth > MaxChatMaxSubagentDepth {
		return xerrors.Errorf("--chat-max-subagent-depth (%d) must be between 1 and %d", depth, MaxChatMaxSubagentDepth)
	}
	return nil
}
