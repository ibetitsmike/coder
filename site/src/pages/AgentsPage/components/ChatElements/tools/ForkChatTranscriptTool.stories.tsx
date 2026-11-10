import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { MockChatTranscriptResult } from "#/testHelpers/forkChatTodos";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const mockPagedChatTranscriptResult = {
	...MockChatTranscriptResult,
	messages: MockChatTranscriptResult.messages.slice(0, 1),
	has_more: true,
};

const mockEmptyChatTranscriptResult = {
	...MockChatTranscriptResult,
	messages: [],
};

const meta: Meta<typeof Tool> = {
	title: "pages/AgentsPage/ChatElements/tools/ForkChatTranscriptTool",
	component: Tool,
	args: {
		name: "read_chat_transcript",
		status: "completed",
		result: MockChatTranscriptResult,
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
		within(canvasElement).getByRole("button", { name: /transcript|message/i }),
	);
};

export const Read: Story = { play: expand };

export const Reading: Story = {
	args: { status: "running", result: undefined },
};

export const Empty: Story = {
	args: { result: mockEmptyChatTranscriptResult },
};

export const EmptyPageHasMore: Story = {
	args: { result: { ...mockEmptyChatTranscriptResult, has_more: true } },
	play: expand,
};

export const HasMore: Story = {
	args: { result: mockPagedChatTranscriptResult },
	play: expand,
};

export const Failed: Story = {
	args: {
		result: {
			error:
				"chat 00000000-0000-0000-0000-000000000000 does not exist or your user cannot read it",
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
	args: { result: { messages: "none" } },
	play: expand,
};
