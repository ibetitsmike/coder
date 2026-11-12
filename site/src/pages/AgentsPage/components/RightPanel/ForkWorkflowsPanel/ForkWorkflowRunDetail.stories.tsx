import type { Meta, StoryObj } from "@storybook/react-vite";
import { spyOn } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	MockChatWorkflowRunDetail,
	MockForkWorkflowNow,
} from "#/testHelpers/forkWorkflows";
import { ForkWorkflowRunDetailView } from "./ForkWorkflowRunDetail";

const meta: Meta<typeof ForkWorkflowRunDetailView> = {
	title: "pages/AgentsPage/RightPanel/ForkWorkflowRunDetail",
	component: ForkWorkflowRunDetailView,
	args: { run: MockChatWorkflowRunDetail, error: null },
	parameters: {
		reactRouter: reactRouterParameters({ routing: { path: "/" } }),
	},
	beforeEach: () => {
		spyOn(Date, "now").mockReturnValue(MockForkWorkflowNow);
	},
	decorators: [
		(Story) => (
			<div className="w-[360px] p-4 font-sans">
				<Story />
			</div>
		),
	],
};

export default meta;
type Story = StoryObj<typeof ForkWorkflowRunDetailView>;

export const Completed: Story = {};

export const Running: Story = {
	args: {
		run: {
			...MockChatWorkflowRunDetail,
			status: "running",
			result: null,
			logs: [],
			finished_at: undefined,
			steps: MockChatWorkflowRunDetail.steps.map((step, index) =>
				index === 0 ? step : { ...step, status: "running", finished_at: undefined },
			),
		},
	},
};

export const Failed: Story = {
	args: {
		run: {
			...MockChatWorkflowRunDetail,
			status: "failed",
			result: null,
			error:
				'step "coderd/users.go" failed: the step ended its turn twice without calling workflow_report',
			steps: MockChatWorkflowRunDetail.steps.slice(0, 2).map((step, index) =>
				index === 1
					? {
							...step,
							status: "failed",
							error: "the step ended its turn twice without calling workflow_report",
						}
					: step,
			),
		},
	},
};

export const Interrupted: Story = {
	args: {
		run: {
			...MockChatWorkflowRunDetail,
			status: "interrupted",
			result: null,
			finished_at: undefined,
		},
	},
};

export const RefreshFailed: Story = {
	args: { error: new Error("Request failed with 500.") },
};
