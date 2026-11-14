import type { Meta, StoryObj } from "@storybook/react-vite";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const mockContextWindowResult = {
	summary:
		"Context window w:2 started from the notes of this chat. No summary was made.",
	source: "automatic",
	context_window: { id: "w:2", trigger: "new_context" },
};

const meta: Meta<typeof Tool> = {
	title: "pages/AgentsPage/ChatElements/tools/ForkContextWindowDivider",
	component: Tool,
	args: {
		name: "chat_summarized",
		status: "completed",
		result: mockContextWindowResult,
		isError: false,
		organizationId: "organization-id",
		mcpServers: [],
		subagentTitles: new Map(),
		subagentVariants: new Map(),
		shellToolDisplayMode: "auto",
		codeDiffDisplayMode: "auto",
	},
	parameters: {
		experiments: ["fork-token-budget"],
		reactRouter: reactRouterParameters({
			routing: { path: "/" },
		}),
	},
	decorators: [
		(Story) => (
			<div className="mx-auto w-full max-w-3xl py-6 font-sans text-xs">
				<Story />
			</div>
		),
		withDashboardProvider,
	],
};
export default meta;
type Story = StoryObj<typeof Tool>;

export const NewContext: Story = {};

export const ContextLimit: Story = {
	args: {
		result: {
			...mockContextWindowResult,
			summary:
				"Context window w:3 started from the notes of this chat. No summary was made.",
			context_window: { id: "w:3", trigger: "context_limit" },
		},
	},
};

export const ExperimentOff: Story = {
	parameters: { experiments: [] },
};
