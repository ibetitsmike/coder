import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	MockNotifyUserArgs,
	MockScheduleWakeupArgs,
	MockScheduleWakeupResult,
	MockWaitForEventsResult,
} from "#/testHelpers/forkChatWakeups";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const meta: Meta<typeof Tool> = {
	title: "pages/AgentsPage/ChatElements/tools/ForkWakeupTools",
	component: Tool,
	args: {
		name: "schedule_wakeup",
		status: "completed",
		args: MockScheduleWakeupArgs,
		result: MockScheduleWakeupResult,
		isError: false,
		organizationId: "organization-id",
		mcpServers: [],
		subagentTitles: new Map(),
		subagentVariants: new Map(),
		shellToolDisplayMode: "auto",
		codeDiffDisplayMode: "auto",
	},
	parameters: {
		experiments: ["fork-chat-wakeups"],
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

export const ScheduleWakeup: Story = { play: expand };

export const ScheduleWakeupCron: Story = {
	args: {
		args: {
			prompt: "Summarize the open pull requests.",
			cron: "0 9 * * 1-5",
		},
		result: {
			id: MockScheduleWakeupResult.id,
			kind: "cron",
			next_run_at: "2099-01-05T09:00:00Z",
			expires_at: "2099-02-01T00:00:00Z",
		},
	},
	play: expand,
};

// While the call runs, the card shows the schedule and prompt from the args.
export const ScheduleWakeupRunning: Story = {
	args: { status: "running", result: undefined },
	play: expand,
};

export const ScheduleWakeupFailed: Story = {
	args: {
		result: { error: "interval_seconds must be at least 60" },
		isError: true,
	},
	play: expand,
};

export const ExperimentOff: Story = {
	parameters: { experiments: [] },
	play: expand,
};

export const WaitForEvents: Story = {
	args: {
		name: "wait_for_events",
		args: { pull_request_url: MockWaitForEventsResult.pull_request.url },
		result: MockWaitForEventsResult,
	},
	play: expand,
};

export const WaitForEventsPullRequestOnly: Story = {
	args: {
		name: "wait_for_events",
		args: { pull_request_url: MockWaitForEventsResult.pull_request.url },
		result: { ...MockWaitForEventsResult, children: [] },
	},
	play: expand,
};

export const WaitForEventsFailed: Story = {
	args: {
		name: "wait_for_events",
		args: {},
		result: { error: "this chat has no child agents that are still working" },
		isError: true,
	},
	play: expand,
};

export const WaitForEventsUnparsableResult: Story = {
	args: { name: "wait_for_events", args: {}, result: { children: "none" } },
	play: expand,
};

export const NotifyUser: Story = {
	args: {
		name: "notify_user",
		args: MockNotifyUserArgs,
		result: { sent: true },
	},
	play: expand,
};

export const NotifyUserFailed: Story = {
	args: {
		name: "notify_user",
		args: MockNotifyUserArgs,
		result: { error: "you can send at most 10 notifications an hour" },
		isError: true,
	},
	play: expand,
};
