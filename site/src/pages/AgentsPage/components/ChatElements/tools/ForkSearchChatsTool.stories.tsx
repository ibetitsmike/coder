import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const mockSearchChatsResult = {
	chats: [
		{
			chat_id: "11111111-1111-4111-8111-111111111111",
			title: "Fix flaky e2e suite",
			status: "waiting",
			shared: true,
		},
		{
			chat_id: "22222222-2222-4222-8222-222222222222",
			title: "Migrate chat sidebar to Tailwind",
			status: "running",
		},
		{
			chat_id: "33333333-3333-4333-8333-333333333333",
			title: "",
			status: "error",
		},
	],
	has_more: false,
};

const mockPagedSearchChatsResult = {
	chats: mockSearchChatsResult.chats.slice(0, 2),
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
		result: mockSearchChatsResult,
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
