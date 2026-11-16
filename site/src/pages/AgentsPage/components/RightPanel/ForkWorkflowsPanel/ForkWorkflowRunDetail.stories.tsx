import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, spyOn, userEvent } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { API } from "#/api/api";
import { getAuthorizationKey } from "#/api/queries/authCheck";
import { chatEntityKey } from "#/api/queries/chats";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockChatWorkflowRunDetail,
	MockForkWorkflowNow,
} from "#/testHelpers/forkWorkflows";
import { ForkWorkflowRunDetailView } from "./ForkWorkflowRunDetail";

const mockRunningRunDetail: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "running",
	title: "",
	result: undefined,
	logs: [],
	finished_at: undefined,
	steps: MockChatWorkflowRunDetail.steps.map((step, index) =>
		index === 0 ? step : { ...step, status: "running", finished_at: undefined },
	),
};

const mockFailedRunDetail: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "failed",
	result: undefined,
	error:
		'step "coderd/users.go" failed: the step ended its turn twice without calling workflow_report',
	steps: MockChatWorkflowRunDetail.steps.slice(0, 2).map((step, index) =>
		index === 1
			? {
					...step,
					status: "failed",
					error:
						"the step ended its turn twice without calling workflow_report",
				}
			: step,
	),
};

const mockInterruptedRunDetail: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "interrupted",
	result: undefined,
	finished_at: undefined,
};

const mockStoppedRunDetail: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "stopped",
	result: undefined,
	error: "stopped by user",
	steps: MockChatWorkflowRunDetail.steps.map((step, index) =>
		index === 0
			? step
			: { ...step, status: "failed", error: "stopped by user" },
	),
};

const meta: Meta<typeof ForkWorkflowRunDetailView> = {
	title: "pages/AgentsPage/RightPanel/ForkWorkflowRunDetail",
	component: ForkWorkflowRunDetailView,
	args: { run: MockChatWorkflowRunDetail, error: null, onBack: fn() },
	parameters: {
		reactRouter: reactRouterParameters({ routing: { path: "/" } }),
		queries: [
			{
				key: chatEntityKey(MockChatWorkflowRunDetail.chat_id),
				data: { ...MockChat, id: MockChatWorkflowRunDetail.chat_id },
			},
			{
				key: getAuthorizationKey({
					checks: {
						canUpdateChat: {
							object: {
								resource_type: "chat",
								owner_id: MockChat.owner_id,
								organization_id: MockChat.organization_id,
							},
							action: "update",
						},
					},
				}),
				data: { canUpdateChat: true },
			},
		],
	},
	beforeEach: () => {
		// Named "spy" so Storybook's actions addon skips logging it: its log
		// line calls Date.now, which would recurse into the mock.
		spyOn(Date, "now").mockName("spy").mockReturnValue(MockForkWorkflowNow);
	},
	decorators: [
		(Story) => (
			<div className="w-[360px] font-sans">
				<Story />
			</div>
		),
	],
};

export default meta;
type Story = StoryObj<typeof ForkWorkflowRunDetailView>;

export const Completed: Story = {};

export const Running: Story = {
	args: { run: mockRunningRunDetail },
};

export const RunningStopConfirm: Story = {
	args: { run: mockRunningRunDetail },
	play: async ({ canvas }) => {
		await userEvent.click(
			canvas.getByRole("button", { name: "Stop workflow run" }),
		);
		await screen.findByRole("dialog", { name: "Stop workflow run" });
	},
};

export const RunningStopPending: Story = {
	args: { run: mockRunningRunDetail },
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "post").mockReturnValue(
			new Promise(() => {}),
		);
	},
	play: async ({ canvas }) => {
		await userEvent.click(
			canvas.getByRole("button", { name: "Stop workflow run" }),
		);
		await userEvent.click(
			await screen.findByRole("button", { name: "Stop run" }),
		);
	},
};

export const Stopped: Story = {
	args: { run: mockStoppedRunDetail },
};

export const Failed: Story = {
	args: { run: mockFailedRunDetail },
};

export const Interrupted: Story = {
	args: { run: mockInterruptedRunDetail },
};

/**
 * Each story lists its own cache seeds so the state the story depends on is
 * visible in the story: the chat's cached copy and its update check. The
 * update check reads the chat seed, so both entries come along.
 */
const writeAccessQueryParameters = (canUpdateChat: boolean) => ({
	queries: [
		{
			key: chatEntityKey(MockChatWorkflowRunDetail.chat_id),
			data: { ...MockChat, id: MockChatWorkflowRunDetail.chat_id },
		},
		{
			key: getAuthorizationKey({
				checks: {
					canUpdateChat: {
						object: {
							resource_type: "chat",
							owner_id: MockChat.owner_id,
							organization_id: MockChat.organization_id,
						},
						action: "update",
					},
				},
			}),
			data: { canUpdateChat },
		},
	],
});

/** An interrupted run with Resume next to the heading. */
export const InterruptedWithResume: Story = {
	args: { run: mockInterruptedRunDetail },
	parameters: writeAccessQueryParameters(true),
};

/** An interrupted run shown to a viewer who may not update the chat. */
export const InterruptedNoWriteAccess: Story = {
	args: { run: mockInterruptedRunDetail },
	parameters: writeAccessQueryParameters(false),
};

export const Loading: Story = {
	args: { run: undefined },
};

export const LoadFailed: Story = {
	args: { run: undefined, error: new Error("Request failed with 404.") },
};

export const RefreshFailed: Story = {
	args: { error: new Error("Request failed with 500.") },
};
