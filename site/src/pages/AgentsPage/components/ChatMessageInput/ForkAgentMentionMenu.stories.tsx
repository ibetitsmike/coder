import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { fn } from "storybook/test";
import {
	AgentMentionOption,
	ForkAgentMentionMenu,
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

export const Empty: Story = {
	args: { options: [] },
};

export const Loading: Story = {
	args: { options: [], status: "Loading agents..." },
};

export const LoadError: Story = {
	args: {
		status: "Could not load some agents. Close and type @ again to retry.",
	},
};
