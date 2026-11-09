import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { MockChatTodoList } from "#/testHelpers/forkChatTodos";
import { ChatTodoPillView } from "./ChatTodoPill";

const meta: Meta<typeof ChatTodoPillView> = {
	title: "pages/AgentsPage/ChatTodoPill",
	component: ChatTodoPillView,
	args: {
		todos: MockChatTodoList.todos,
		error: undefined,
	},
};

export default meta;
type Story = StoryObj<typeof ChatTodoPillView>;

export const Collapsed: Story = {};

export const Expanded: Story = {
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", { name: /Todo list/ }),
		);
	},
};

export const LongContent: Story = {
	args: {
		todos: [
			...MockChatTodoList.todos,
			{
				id: "long",
				content:
					"Trace every caller of the transcript reader, confirm each one passes the owner's credentials, and note the callers that read a descendant chat through the parent walk",
				status: "pending",
			},
		],
	},
	play: Expanded.play,
};

export const LoadError: Story = {
	args: {
		todos: undefined,
		error: mockApiError({ message: "You do not have access to this chat." }),
	},
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", {
				name: "Todo list failed to load",
			}),
		);
	},
};
