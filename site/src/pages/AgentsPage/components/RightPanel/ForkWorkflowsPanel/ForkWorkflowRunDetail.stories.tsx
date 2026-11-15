import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, spyOn, userEvent } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { API } from "#/api/api";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
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

const mockPhasedRunDetail: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "running",
	result: undefined,
	logs: [],
	finished_at: undefined,
	phases: ["review", "summarize", "publish"],
	current_phase: "summarize",
	steps: MockChatWorkflowRunDetail.steps.map((step, index) => ({
		...step,
		reused: false,
		status: index === 2 ? "running" : "reported",
		phase: index === 2 ? "summarize" : "review",
		finished_at: index === 2 ? undefined : step.finished_at,
	})),
};

const meta: Meta<typeof ForkWorkflowRunDetailView> = {
	title: "pages/AgentsPage/RightPanel/ForkWorkflowRunDetail",
	component: ForkWorkflowRunDetailView,
	args: { run: MockChatWorkflowRunDetail, error: null, onBack: fn() },
	parameters: {
		reactRouter: reactRouterParameters({ routing: { path: "/" } }),
		// The stories spy on Date.now. Storybook's actions addon logs every
		// mock call, and its log line calls Date.now, so the spy recurses.
		actions: { disable: true },
	},
	beforeEach: () => {
		spyOn(Date, "now").mockReturnValue(MockForkWorkflowNow);
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
		await userEvent.click(canvas.getByRole("button", { name: "Stop" }));
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
		await userEvent.click(canvas.getByRole("button", { name: "Stop" }));
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

/** The rail shows declared phases the run has not reached yet. */
export const RunningWithPhases: Story = {
	args: { run: mockPhasedRunDetail },
};

/** A run without phases renders as before, with no rail. */
export const CompletedWithoutPhases: Story = {
	args: {
		run: {
			...MockChatWorkflowRunDetail,
			phases: [],
			current_phase: "",
			steps: MockChatWorkflowRunDetail.steps.map((step) => ({
				...step,
				phase: "",
			})),
		},
	},
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
