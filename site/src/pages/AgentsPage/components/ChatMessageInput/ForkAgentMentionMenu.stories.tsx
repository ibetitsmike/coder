import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { fn } from "storybook/test";
import {
	MockForkOrganizationSavedWorkflowMetadata,
	MockForkSavedWorkflowMetadata,
} from "#/testHelpers/forkSavedWorkflows";
import {
	AgentMentionOption,
	ForkAgentMentionMenu,
	type WorkflowMention,
	WorkflowMentionOption,
} from "./ForkAgentMentionMenu";

const mockAgentMentions = [
	{ name: "explore", source: "built-in", description: "Code discovery." },
	{ name: "personal/reviewer", source: "personal", description: "Reviews." },
	{ name: "workspace/reviewer", source: "workspace", description: "" },
] as const;

const meta: Meta<typeof ForkAgentMentionMenu> = {
	title: "components/ChatMessageInput/ForkAgentMentionMenu",
	component: ForkAgentMentionMenu,
	args: {
		options: mockAgentMentions.map((agent) => new AgentMentionOption(agent)),
		selectedIndex: 0,
		onSelect: fn(),
		onHighlight: fn(),
	},
	render: function Render(args) {
		const [anchor, setAnchor] = useState<HTMLDivElement | null>(null);
		return (
			<div className="h-96 p-6">
				<div ref={setAnchor} className="mt-64 h-16 w-80 rounded-md border" />
				<ForkAgentMentionMenu {...args} anchor={anchor} />
			</div>
		);
	},
};

export default meta;
type Story = StoryObj<typeof ForkAgentMentionMenu>;

export const AgentList: Story = {};

const mockWorkflowMentions = [
	{
		name: MockForkSavedWorkflowMetadata.name,
		tier: "personal",
		description: MockForkSavedWorkflowMetadata.description,
		argsSummary: "files (required)",
	},
	{
		name: MockForkOrganizationSavedWorkflowMetadata.name,
		tier: "org",
		description: MockForkOrganizationSavedWorkflowMetadata.description,
		argsSummary: "",
	},
] as const satisfies readonly WorkflowMention[];

export const AgentsAndWorkflows: Story = {
	args: {
		options: [
			...mockAgentMentions.map((agent) => new AgentMentionOption(agent)),
			...mockWorkflowMentions.map(
				(workflow) => new WorkflowMentionOption(workflow),
			),
		],
		workflowStartIndex: mockAgentMentions.length,
	},
};

export const WorkflowsOnly: Story = {
	args: {
		options: mockWorkflowMentions.map(
			(workflow) => new WorkflowMentionOption(workflow),
		),
		workflowStartIndex: 0,
	},
};

export const Empty: Story = {
	args: { options: [] },
};

export const AgentsOnlyEmpty: Story = {
	args: {
		options: [],
		// The empty message the menu shows while fork-workflows is off.
		emptyMessage: "No agents match that query.",
	},
};

export const WorkflowsOnlyEmpty: Story = {
	args: {
		options: [],
		// The empty message the menu shows while fork-agent-definitions
		// is off.
		emptyMessage: "No workflows match that query.",
	},
};

export const Loading: Story = {
	args: { options: [], status: "Loading agents..." },
};

export const LoadingWorkflows: Story = {
	args: { options: [], status: "Loading workflows..." },
};

export const LoadingAgentsAndWorkflows: Story = {
	args: { options: [], status: "Loading agents and workflows..." },
};

export const LoadError: Story = {
	args: {
		status: "Could not load some agents. Close and type @ again to retry.",
	},
};

export const WorkflowLoadError: Story = {
	args: {
		status: "Could not load some workflows. Close and type @ again to retry.",
	},
};

export const AgentsAndWorkflowsLoadError: Story = {
	args: {
		status:
			"Could not load some agents and workflows. Close and type @ again to retry.",
	},
};
