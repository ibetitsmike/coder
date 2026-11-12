import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, spyOn } from "storybook/test";
import {
	MockChatWorkflowRun,
	MockForkWorkflowNow,
} from "#/testHelpers/forkWorkflows";
import { ForkWorkflowRunList } from "./ForkWorkflowRunList";

const meta: Meta<typeof ForkWorkflowRunList> = {
	title: "pages/AgentsPage/RightPanel/ForkWorkflowRunList",
	component: ForkWorkflowRunList,
	args: {
		runs: [
			{
				...MockChatWorkflowRun,
				id: "run-running",
				status: "running",
				title: "",
				result: null,
				finished_at: undefined,
				created_at: "2026-11-12T10:04:10Z",
				step_counts: { total: 3, running: 2, done: 1, failed: 0 },
			},
			MockChatWorkflowRun,
			{
				...MockChatWorkflowRun,
				id: "run-failed",
				status: "failed",
				title: "Fix flaky tests",
				step_counts: { total: 2, running: 0, done: 1, failed: 1 },
			},
			{
				...MockChatWorkflowRun,
				id: "run-interrupted",
				status: "interrupted",
				title: "Migrate the handlers",
				finished_at: undefined,
			},
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
