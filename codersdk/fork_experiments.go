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
	ExperimentForkSubagentIsolation,
	ExperimentForkChatWakeups,
	ExperimentForkPluginPacks,
	ExperimentForkPinnedSkills,
	ExperimentForkWorkflows,
	ExperimentForkFamilyMessages,
	ExperimentForkHTMLAttachments,
	ExperimentForkTokenBudget,
	ExperimentForkViewImage,
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

// ExperimentForkSubagentIsolation lets spawn_agent run a child in its own git
// worktree.
const ExperimentForkSubagentIsolation Experiment = "fork-subagent-isolation"

// ExperimentForkChatWakeups lets chats schedule their own wake-ups: the
// schedule_wakeup and wait_for_events tools, /loop, and the wake-up worker.
const ExperimentForkChatWakeups Experiment = "fork-chat-wakeups"

// ExperimentForkPluginPacks enables plugin packs: workspace pack discovery with
// manifest-honored skills and agents, installed packs, and (in later PRs)
// their rules and surfacing controls.
const ExperimentForkPluginPacks Experiment = "fork-plugin-packs"

// ExperimentForkPinnedSkills lets a user pin skills to a chat, so chatd puts
// their bodies in the prompt of every turn until they are unpinned.
const ExperimentForkPinnedSkills Experiment = "fork-pinned-skills"

// ExperimentForkWorkflows enables the run_workflow and await_workflow tools
// for root chats: a Starlark script runs sub-agent steps and combines their
// results (fork PR 1a ships the report tool and step plumbing).
const ExperimentForkWorkflows Experiment = "fork-workflows"

// ExperimentForkFamilyMessages lets a chat message its parent, siblings and
// other chats in its family with the message_family_chat tool.
const ExperimentForkFamilyMessages Experiment = "fork-family-messages"

// ExperimentForkHTMLAttachments renders an HTML file that a chat attaches in
// a sandboxed viewer, from which a click can send a message to the chat.
const ExperimentForkHTMLAttachments Experiment = "fork-html-attachments"

// ExperimentForkTokenBudget gives chats token-budget context windows: the
// notes tool keeps a checkpoint, and a chat starts a new context window from
// it instead of summarizing its history.
const ExperimentForkTokenBudget Experiment = "fork-token-budget"

// ExperimentForkViewImage enables the view_image chat tool, which loads a PNG,
// JPEG, GIF or WebP file from the workspace into the model's context.
const ExperimentForkViewImage Experiment = "fork-view-image"

// forkExperimentDisplayNames names the fork experiments whose title-cased
// names read wrong, such as "Html".
var forkExperimentDisplayNames = map[Experiment]string{
	ExperimentForkHTMLAttachments: "Fork HTML Attachments",
}

// forkExperimentDisplayName returns the display name of a fork experiment
// that has one. Experiment.DisplayName calls it first.
func forkExperimentDisplayName(e Experiment) (string, bool) {
	name, ok := forkExperimentDisplayNames[e]
	return name, ok
}

// init adds the fork experiments to the known list so the CLI flag, the
// deployment config and the experiment evaluator accept them the same way they
// accept upstream experiments. Package variables initialize before init
// functions, so ExperimentsKnown is already populated here.
func init() {
	ExperimentsKnown = append(ExperimentsKnown, ForkExperiments...)
}
