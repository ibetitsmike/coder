import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { MockSearchChatsResult } from "#/testHelpers/forkChatTodos";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const mockPagedSearchChatsResult = {
	chats: MockSearchChatsResult.chats.slice(0, 2),
	has_more: true,
};

const mockEmptySearchChatsResult = { chats: [], has_more: false };

const meta: Meta<typeof Tool> = {
	title: "pages/AgentsPage/ChatElements/tools/ForkSearchChatsTool",
	component: Tool,
	args: {
		name: "search_chats",
		status: "completed",
		args: { query: "status:waiting" },
		result: MockSearchChatsResult,
		isError: false,
		organizationId: "organization-id",
		mcpServers: [],
		subagentTitles: new Map(),
		subagentVariants: new Map(),
		shellToolDisplayMode: "auto",
		codeDiffDisplayMode: "auto",
	},
	parameters: {
		experiments: ["fork-chat-todos"],
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

const expand: Story["play"] = async ({ canvasElement }) => {
	await userEvent.click(
		within(canvasElement).getByRole("button", { name: /chat/i }),
	);
};

export const Found: Story = { play: expand };

// While the search runs, the card shows the streamed query.
export const Running: Story = {
	args: { status: "running", result: undefined },
	play: expand,
};

export const Empty: Story = {
	args: { args: { query: "status:error" }, result: mockEmptySearchChatsResult },
};

export const HasMore: Story = {
	args: { result: mockPagedSearchChatsResult },
	play: expand,
};

export const Failed: Story = {
	args: {
		result: {
			error: "invalid query: title: search cannot be combined with title",
		},
		isError: true,
	},
	play: expand,
};

export const ExperimentOff: Story = {
	parameters: { experiments: [] },
	play: expand,
};

export const UnparsableResult: Story = {
	args: { result: { chats: "none" } },
	play: expand,
};
