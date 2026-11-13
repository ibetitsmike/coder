import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	MockListFamilyChatsResult,
	MockMessageFamilyChatArgs,
	MockMessageFamilyChatResult,
} from "#/testHelpers/forkFamilyMessages";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const meta: Meta<typeof Tool> = {
	title: "pages/AgentsPage/ChatElements/tools/ForkFamilyMessageTool",
	component: Tool,
	args: {
		name: "message_family_chat",
		status: "completed",
		args: MockMessageFamilyChatArgs,
		result: MockMessageFamilyChatResult,
		isError: false,
		organizationId: "organization-id",
		mcpServers: [],
		subagentTitles: new Map(),
		subagentVariants: new Map(),
		shellToolDisplayMode: "auto",
		codeDiffDisplayMode: "auto",
	},
	parameters: {
		experiments: ["fork-family-messages"],
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
	await userEvent.click(within(canvasElement).getByRole("button"));
};

export const Sent: Story = { play: expand };

export const Queued: Story = {
	args: { result: { ...MockMessageFamilyChatResult, queued: true } },
	play: expand,
};

// While the send runs, the card shows the streamed message.
export const Sending: Story = {
	args: { status: "running", result: undefined },
	play: expand,
};

export const Failed: Story = {
	args: {
		result: "chat 22222222 is not in your chat family",
		isError: true,
	},
	play: expand,
};

export const ListFamilyChats: Story = {
	args: { name: "list_family_chats", args: {}, result: MockListFamilyChatsResult },
	play: expand,
};
