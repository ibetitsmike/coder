import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, userEvent, within } from "storybook/test";
import type { ForkAgentType } from "#/api/typesGenerated";
import { ForkCompactAgentTypeSelector } from "./ForkCompactAgentTypeSelector";

const mockAgentType = (
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

const mockAgentTypes: ForkAgentType[] = [
	mockAgentType("general", "builtin", "Does the task with the full tool set."),
	mockAgentType("explore", "builtin", "Reads the code and reports, read-only."),
	mockAgentType("personal/reviewer", "personal", "Reviews the diff."),
	mockAgentType("org/reviewer", "org", "Reviews the diff against team rules."),
	mockAgentType("deployer", "project", "Deploys.", "Its model is unavailable."),
	mockAgentType(
		"tester",
		"workspace",
		"Runs the test suite.",
		"agent definitions from the workspace can only be used through spawn_agent for now",
	),
];

const meta: Meta<typeof ForkCompactAgentTypeSelector> = {
	title: "pages/AgentsPage/ChatElements/ForkCompactAgentTypeSelector",
	component: ForkCompactAgentTypeSelector,
	args: {
		value: null,
		agentTypes: mockAgentTypes,
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
	play: openSelector,
};

export const OpenLoading: Story = {
	args: { agentTypes: [], isLoading: true },
	play: openSelector,
};

export const OpenError: Story = {
	args: { agentTypes: [], error: new Error("Failed to load agent types.") },
	play: openSelector,
};
