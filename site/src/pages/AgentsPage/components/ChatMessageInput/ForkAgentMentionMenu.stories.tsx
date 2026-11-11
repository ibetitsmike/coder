import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, fn, userEvent } from "storybook/test";
import {
	AgentMentionOption,
	ForkAgentMentionMenu,
} from "./ForkAgentMentionMenu";
import { findVisibleText } from "./storyHelpers";

const options = [
	{ name: "explore", source: "built-in", description: "Code discovery." },
	{ name: "personal/reviewer", source: "personal", description: "Reviews." },
	{ name: "workspace/reviewer", source: "workspace", description: "" },
] as const;

const meta: Meta<typeof ForkAgentMentionMenu> = {
	title: "components/ChatMessageInput/ForkAgentMentionMenu",
	component: ForkAgentMentionMenu,
	args: {
		options: options.map((agent) => new AgentMentionOption(agent)),
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

export const AgentList: Story = {
	play: async ({ args }) => {
		await findVisibleText("workspace");
		await userEvent.click(await findVisibleText("@personal/reviewer"));
		await expect(args.onSelect).toHaveBeenCalledWith(args.options?.[1]);
	},
};

export const Empty: Story = {
	args: { options: [] },
	play: async () => {
		await findVisibleText("No agents match that query.");
	},
};

export const Loading: Story = {
	args: { options: [], status: "Loading agents..." },
	play: async () => {
		await findVisibleText("Loading agents...");
	},
};

export const LoadError: Story = {
	args: {
		status: "Could not load some agents. Close and type @ again to retry.",
	},
	play: async () => {
		await findVisibleText(
			"Could not load some agents. Close and type @ again to retry.",
		);
	},
};
