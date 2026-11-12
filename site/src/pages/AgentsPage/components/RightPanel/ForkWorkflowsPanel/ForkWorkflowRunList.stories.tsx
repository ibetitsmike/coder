import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, spyOn } from "storybook/test";
import type { WorkflowRun } from "#/api/typesGenerated";
import {
	MockChatWorkflowRun,
	MockForkWorkflowNow,
} from "#/testHelpers/forkWorkflows";
import { ForkWorkflowRunList } from "./ForkWorkflowRunList";

const mockRunningRun: WorkflowRun = {
	...MockChatWorkflowRun,
	id: "run-running",
	status: "running",
	title: "",
	finished_at: undefined,
	created_at: "2026-11-12T10:04:10Z",
	step_counts: { total: 3, running: 2, done: 1, failed: 0 },
};

const mockFailedRun: WorkflowRun = {
	...MockChatWorkflowRun,
	id: "run-failed",
	status: "failed",
	title: "Fix flaky tests",
	step_counts: { total: 2, running: 0, done: 1, failed: 1 },
};

const mockInterruptedRun: WorkflowRun = {
	...MockChatWorkflowRun,
	id: "run-interrupted",
	status: "interrupted",
	title: "Migrate the handlers",
	finished_at: undefined,
};

const mockStoppedRun: WorkflowRun = {
	...MockChatWorkflowRun,
	id: "run-stopped",
	status: "stopped",
	title: "Rename the audit fields",
	error: "stopped by user",
	step_counts: { total: 2, running: 0, done: 1, failed: 1 },
};

const meta: Meta<typeof ForkWorkflowRunList> = {
	title: "pages/AgentsPage/RightPanel/ForkWorkflowRunList",
	component: ForkWorkflowRunList,
	args: {
		runs: [
			mockRunningRun,
			MockChatWorkflowRun,
			mockFailedRun,
			mockInterruptedRun,
			mockStoppedRun,
		],
		error: null,
		onSelectRun: fn(),
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
type Story = StoryObj<typeof ForkWorkflowRunList>;

export const Runs: Story = {};

export const Loading: Story = {
	args: { runs: undefined },
};

export const LoadFailed: Story = {
	args: { runs: undefined, error: new Error("Request failed with 500.") },
};

export const RefreshFailed: Story = {
	args: { error: new Error("Request failed with 500.") },
};
