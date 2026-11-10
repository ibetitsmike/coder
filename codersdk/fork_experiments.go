package codersdk

// This file is fork code: it registers the fork-only experiments so upstream
// files never need to list them. Add a fork experiment here instead of editing
// ExperimentsKnown in deployment.go.

// ForkExperiments are the experiments that exist only in this fork. Each one
// is named "fork-<feature>", which keeps a fork experiment from colliding with
// an upstream experiment name now or after a sync. Every fork feature sits
// behind one of these experiments, so a deployment with no fork experiment
// enabled behaves exactly like upstream.
var ForkExperiments = Experiments{
	ExperimentForkChatTodos,
	ExperimentForkNestedSubagents,
	ExperimentForkAgentDefinitions,
	ExperimentForkChatWakeups,
}

// ExperimentForkChatTodos enables the todo_write and todo_read chat tools and
// the chat todo list.
const ExperimentForkChatTodos Experiment = "fork-chat-todos"

// ExperimentForkNestedSubagents lets general subagents spawn their own
// subagents up to the maximum subagent depth.
const ExperimentForkNestedSubagents Experiment = "fork-nested-subagents"

// ExperimentForkAgentDefinitions lets spawn_agent use the custom agent types
// that workspace agent definition files declare.
const ExperimentForkAgentDefinitions Experiment = "fork-agent-definitions"

// ExperimentForkChatWakeups lets chats schedule their own wake-ups: the
// schedule_wakeup and wait_for_events tools, /loop, and the wake-up worker.
const ExperimentForkChatWakeups Experiment = "fork-chat-wakeups"

// init adds the fork experiments to the known list so the CLI flag, the
// deployment config and the experiment evaluator accept them the same way they
// accept upstream experiments. Package variables initialize before init
// functions, so ExperimentsKnown is already populated here.
func init() {
	ExperimentsKnown = append(ExperimentsKnown, ForkExperiments...)
}
