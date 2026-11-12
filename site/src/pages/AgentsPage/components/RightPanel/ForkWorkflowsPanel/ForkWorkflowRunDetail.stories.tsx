import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, spyOn } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
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

const meta: Meta<typeof ForkWorkflowRunDetailView> = {
	title: "pages/AgentsPage/RightPanel/ForkWorkflowRunDetail",
	component: ForkWorkflowRunDetailView,
	args: { run: MockChatWorkflowRunDetail, error: null, onBack: fn() },
	parameters: {
		reactRouter: reactRouterParameters({ routing: { path: "/" } }),
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

export const Failed: Story = {
	args: { run: mockFailedRunDetail },
};

export const Interrupted: Story = {
	args: { run: mockInterruptedRunDetail },
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
