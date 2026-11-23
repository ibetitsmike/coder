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
	// MaxConcurrentSubagents is the maximum number of running subagent chats
	// in the deployment, not counting a subagent's own ancestors. 0 means no
	// limit.
	MaxConcurrentSubagents serpent.Int64 `json:"max_concurrent_subagents" typescript:",notnull"`
	// MaxConcurrentSubagentsPerFamily is the maximum number of running
	// subagent chats under one root chat, not counting a subagent's own
	// ancestors. 0 means no limit.
	MaxConcurrentSubagentsPerFamily serpent.Int64 `json:"max_concurrent_subagents_per_family" typescript:",notnull"`
	// MaxLiveSubagentsPerFamily is the maximum number of live subagent chats
	// under one root chat, checked when a subagent is spawned or when
	// message_agent resumes a finished one. 0 means no limit.
	MaxLiveSubagentsPerFamily serpent.Int64 `json:"max_live_subagents_per_family" typescript:",notnull"`
	// APNsKeyFile is the path of the APNs auth key (.p8) that signs the
	// pushes of the fork-apns-push experiment. Empty turns APNs off. The
	// key itself never appears in the deployment config.
	APNsKeyFile serpent.String `json:"apns_key_file" typescript:",notnull"`
	// APNsKeyID is the 10-character ID of the APNs auth key.
	APNsKeyID serpent.String `json:"apns_key_id" typescript:",notnull"`
	// APNsTeamID is the Apple Developer team ID that owns the key.
	APNsTeamID serpent.String `json:"apns_team_id" typescript:",notnull"`
	// APNsTopic is the bundle ID of the app that receives the pushes. It
	// has no default: an APNs key can push only to the apps of its own
	// team, so the operator names the app the key belongs with.
	APNsTopic serpent.String `json:"apns_topic" typescript:",notnull"`
}

// APNsConfigured reports whether any APNs key option is set.
func (c ForkChatConfig) APNsConfigured() bool {
	return c.APNsKeyFile.Value() != "" || c.APNsKeyID.Value() != "" || c.APNsTeamID.Value() != ""
}

// APNsMissing lists the flags of the APNs options that are empty. APNs
// needs them all. The server never refuses to start over them: coderd logs
// what is missing and leaves APNs off.
func (c ForkChatConfig) APNsMissing() []string {
	var missing []string
	for _, opt := range []struct {
		flag  string
		value string
	}{
		{"--chat-apns-key-file", c.APNsKeyFile.Value()},
		{"--chat-apns-key-id", c.APNsKeyID.Value()},
		{"--chat-apns-team-id", c.APNsTeamID.Value()},
		{"--chat-apns-topic", c.APNsTopic.Value()},
	} {
		if opt.value == "" {
			missing = append(missing, opt.flag)
		}
	}
	return missing
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
		{
			Name:        "Chat: Max Concurrent Subagents",
			Description: "Maximum number of running subagent chats in the deployment when the fork-nested-subagents experiment is enabled. A subagent's own ancestors do not count against it, so nested subagents can exceed the limit. 0 means no limit. An organization can set a lower value for its own chats.",
			Flag:        "chat-max-concurrent-subagents",
			Env:         "CODER_CHAT_MAX_CONCURRENT_SUBAGENTS",
			Value:       &c.AI.Chat.Fork.MaxConcurrentSubagents,
			Default:     "0",
			Group:       group,
			YAML:        "maxConcurrentSubagents",
		},
		{
			Name:        "Chat: Max Concurrent Subagents Per Family",
			Description: "Maximum number of running subagent chats under one root chat when the fork-nested-subagents experiment is enabled. A subagent's own ancestors do not count against it, so nested subagents can exceed the limit. 0 means no limit. An organization can set a lower value.",
			Flag:        "chat-max-concurrent-subagents-per-family",
			Env:         "CODER_CHAT_MAX_CONCURRENT_SUBAGENTS_PER_FAMILY",
			Value:       &c.AI.Chat.Fork.MaxConcurrentSubagentsPerFamily,
			Default:     "0",
			Group:       group,
			YAML:        "maxConcurrentSubagentsPerFamily",
		},
		{
			Name:        "Chat: Max Live Subagents Per Family",
			Description: "Maximum number of live subagent chats under one root chat when the fork-nested-subagents experiment is enabled. A live subagent has not finished and is not archived. Spawning a subagent, or resuming a finished one with message_agent, fails at the limit. 0 means no limit. An organization can set a lower value.",
			Flag:        "chat-max-live-subagents-per-family",
			Env:         "CODER_CHAT_MAX_LIVE_SUBAGENTS_PER_FAMILY",
			Value:       &c.AI.Chat.Fork.MaxLiveSubagentsPerFamily,
			Default:     "0",
			Group:       group,
			YAML:        "maxLiveSubagentsPerFamily",
		},
		{
			Name:        "Chat: APNs Key File",
			Description: "Path of the APNs auth key (.p8) that signs the chat pushes sent to the iOS app when the fork-apns-push experiment is enabled. Empty turns APNs off. Needs --chat-apns-key-id, --chat-apns-team-id and --chat-apns-topic.",
			Flag:        "chat-apns-key-file",
			Env:         "CODER_CHAT_APNS_KEY_FILE",
			Value:       &c.AI.Chat.Fork.APNsKeyFile,
			Group:       group,
			YAML:        "apnsKeyFile",
		},
		{
			Name:        "Chat: APNs Key ID",
			Description: "The 10-character ID of the APNs auth key in --chat-apns-key-file.",
			Flag:        "chat-apns-key-id",
			Env:         "CODER_CHAT_APNS_KEY_ID",
			Value:       &c.AI.Chat.Fork.APNsKeyID,
			Group:       group,
			YAML:        "apnsKeyID",
		},
		{
			Name:        "Chat: APNs Team ID",
			Description: "The Apple Developer team ID that owns the APNs auth key in --chat-apns-key-file.",
			Flag:        "chat-apns-team-id",
			Env:         "CODER_CHAT_APNS_TEAM_ID",
			Value:       &c.AI.Chat.Fork.APNsTeamID,
			Group:       group,
			YAML:        "apnsTeamID",
		},
		{
			Name:        "Chat: APNs Topic",
			Description: "The bundle ID of the iOS app that receives the chat pushes. The app must belong to the team of the APNs key.",
			Flag:        "chat-apns-topic",
			Env:         "CODER_CHAT_APNS_TOPIC",
			Value:       &c.AI.Chat.Fork.APNsTopic,
			Group:       group,
			YAML:        "apnsTopic",
		},
	}
}

// validateForkChat runs from Validate rather than as a per-option validator,
// which only fires in Set and so misses the YAML path.
func (c *DeploymentValues) validateForkChat() error {
	if depth := c.AI.Chat.Fork.MaxSubagentDepth.Value(); depth < 1 || depth > MaxChatMaxSubagentDepth {
		return xerrors.Errorf("--chat-max-subagent-depth (%d) must be between 1 and %d", depth, MaxChatMaxSubagentDepth)
	}
	for _, limit := range []struct {
		flag  string
		value int64
	}{
		{"chat-max-concurrent-subagents", c.AI.Chat.Fork.MaxConcurrentSubagents.Value()},
		{"chat-max-concurrent-subagents-per-family", c.AI.Chat.Fork.MaxConcurrentSubagentsPerFamily.Value()},
		{"chat-max-live-subagents-per-family", c.AI.Chat.Fork.MaxLiveSubagentsPerFamily.Value()},
	} {
		if limit.value < 0 {
			return xerrors.Errorf("--%s (%d) must be 0 or greater", limit.flag, limit.value)
		}
	}
	return nil
}
