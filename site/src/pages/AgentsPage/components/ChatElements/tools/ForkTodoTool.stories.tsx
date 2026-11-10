import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import type { ChatTodoList } from "#/api/typesGenerated";
import { MockChatTodoList } from "#/testHelpers/forkChatTodos";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const meta: Meta<typeof Tool> = {
	title: "pages/AgentsPage/ChatElements/tools/ForkTodoTool",
	component: Tool,
	args: {
		name: "todo_write",
		status: "completed",
		args: { todos: MockChatTodoList.todos },
		result: MockChatTodoList,
		isError: false,
		organizationId: "organization-id",
		mcpServers: [],
		subagentTitles: new Map(),
		subagentVariants: new Map(),
		shellToolDisplayMode: "auto",
		codeDiffDisplayMode: "auto",
	},
	parameters: { experiments: ["fork-chat-todos"] },
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

const mockEmptyChatTodoList: ChatTodoList = { ...MockChatTodoList, todos: [] };

const expand: Story["play"] = async ({ canvasElement }) => {
	await userEvent.click(
		within(canvasElement).getByRole("button", { name: /todo/i }),
	);
};

export const Updated: Story = { play: expand };

// Once its args finish streaming, a running todo_write lists them.
export const Updating: Story = {
	args: { status: "running", result: undefined },
	play: expand,
};

export const Cleared: Story = {
	args: { args: { todos: [] }, result: mockEmptyChatTodoList },
};

export const Read: Story = {
	args: { name: "todo_read", args: {} },
	play: expand,
};

export const ReadRunning: Story = {
	args: { name: "todo_read", args: {}, status: "running", result: undefined },
};

export const ReadEmpty: Story = {
	args: { name: "todo_read", args: {}, result: mockEmptyChatTodoList },
};

export const Failed: Story = {
	args: {
		args: {
			todos: [
				{ id: "a", content: "Buy bread", status: "pending" },
				{ id: "a", content: "Buy milk", status: "pending" },
			],
		},
		result: { error: 'todo "a": id appears more than once' },
		isError: true,
	},
	play: expand,
};

export const ReadFailed: Story = {
	args: {
		name: "todo_read",
		args: {},
		result: { error: "chat not found" },
		isError: true,
	},
	play: expand,
};

export const ExperimentOff: Story = {
	parameters: { experiments: [] },
	play: expand,
};

export const UnparsableResult: Story = {
	args: { name: "todo_read", args: {}, result: { todos: "none" } },
	play: expand,
};
