package coderd

// This file is fork code: it turns on the process events that workspace
// agents report for the fork-chat-wakeups experiment. Upstream does not
// have it.

import (
	"github.com/coder/coder/v2/coderd/agentapi"
	"github.com/coder/coder/v2/codersdk"
)

// forkChatProcessEvents returns the agent API options of the process events
// that agents report, nil while the fork-chat-wakeups experiment is off. A
// replica without a chat daemon still records the events, which the
// wake-up sweep of another replica then finds.
func (api *API) forkChatProcessEvents() *agentapi.ForkChatProcessEvents {
	if !api.Experiments.Enabled(codersdk.ExperimentForkChatWakeups) {
		return nil
	}
	events := &agentapi.ForkChatProcessEvents{}
	if api.chatDaemon != nil {
		events.Poke = api.chatDaemon.ForkPokeChatWakeups
	}
	return events
}
