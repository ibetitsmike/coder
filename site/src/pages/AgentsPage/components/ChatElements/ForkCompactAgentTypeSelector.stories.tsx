import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, within } from "storybook/test";
import type { ForkAgentType } from "#/api/typesGenerated";
import { ForkCompactAgentTypeSelector } from "./ForkCompactAgentTypeSelector";

const agentType = (
	name: string,
	source: string,
	description: string,
	unavailableReason?: string,
): ForkAgentType => ({
	name,
	source,
	description,
	base_type: "general",
	root_available: unavailableReason === undefined,
	root_unavailable_reason: unavailableReason,
});

const agentTypes: ForkAgentType[] = [
	agentType("general", "builtin", "Does the task with the full tool set."),
	agentType("explore", "builtin", "Reads the code and reports, read-only."),
	agentType("personal/reviewer", "personal", "Reviews the diff."),
	agentType("org/reviewer", "org", "Reviews the diff against team rules."),
	agentType("deployer", "project", "Deploys.", "Its model is unavailable."),
	agentType("tester", "workspace", "Runs the test suite."),
];

const meta: Meta<typeof ForkCompactAgentTypeSelector> = {
	title: "pages/AgentsPage/ChatElements/ForkCompactAgentTypeSelector",
	component: ForkCompactAgentTypeSelector,
	args: {
		value: null,
		agentTypes,
		onChange: fn(),
		onRetry: fn(),
	},
};

export default meta;
type Story = StoryObj<typeof ForkCompactAgentTypeSelector>;

const openSelector: Story["play"] = async ({ canvasElement }) => {
	const canvas = within(canvasElement);
	await userEvent.click(canvas.getByRole("button", { name: /^Agent type:/ }));
	await screen.findByRole("link", { name: "Manage agent definitions…" });
};

export const DefaultAgent: Story = {};

export const TypeSelected: Story = {
	args: { value: "personal/reviewer" },
};

export const Open: Story = {
	args: { value: "explore" },
	play: async (context) => {
		await openSelector(context);
		await expect(
			screen.getByRole("option", { name: /deployer/ }),
		).toHaveAttribute("aria-disabled", "true");
	},
};

export const OpenLoading: Story = {
	args: { agentTypes: [], isLoading: true },
	play: openSelector,
};

export const OpenError: Story = {
	args: { agentTypes: [], error: new Error("Failed to load agent types.") },
	play: openSelector,
};
