import type { Meta, StoryObj } from "@storybook/react-vite";
import { spyOn, userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { API } from "#/api/api";
import {
	MockChatWorkflowRunDetail,
	MockForkWorkflowArgs,
	MockForkWorkflowChatId,
	MockForkWorkflowCompletedResult,
	MockForkWorkflowFailedResult,
	MockForkWorkflowRunId,
	MockForkWorkflowRunningResult,
	MockForkWorkflowScript,
	MockForkWorkflowStartedResult,
} from "#/testHelpers/forkWorkflows";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const meta: Meta<typeof Tool> = {
	title: "pages/AgentsPage/ChatElements/tools/ForkWorkflowTool",
	component: Tool,
	args: {
		name: "await_workflow",
		status: "completed",
		args: { run_id: MockForkWorkflowRunId },
		result: MockForkWorkflowCompletedResult,
		isError: false,
		organizationId: "organization-id",
		mcpServers: [],
		subagentTitles: new Map(),
		subagentVariants: new Map(),
		shellToolDisplayMode: "auto",
		codeDiffDisplayMode: "auto",
	},
	parameters: {
		experiments: ["fork-workflows"],
		reactRouter: reactRouterParameters({ routing: { path: "/" } }),
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
		within(canvasElement).getByRole("button", { name: /workflow/i }),
	);
};

const mockStoppedResult = {
	...MockForkWorkflowRunningResult,
	status: "stopped",
	error: "stopped by user",
	steps: MockForkWorkflowRunningResult.steps.map((step, index) =>
		index === 0
			? step
			: { ...step, status: "failed", error: "stopped by user" },
	),
};

const mockRunWorkflowArgs = {
	script: MockForkWorkflowScript,
	args: MockForkWorkflowArgs,
};

export const Started: Story = {
	args: {
		name: "run_workflow",
		args: mockRunWorkflowArgs,
		result: MockForkWorkflowStartedResult,
	},
	play: expand,
};

export const Starting: Story = {
	args: {
		name: "run_workflow",
		status: "running",
		args: mockRunWorkflowArgs,
		result: undefined,
	},
};

export const Resumed: Story = {
	args: {
		name: "run_workflow",
		args: {
			script_path: "/home/coder/workflows/review.star",
			args: MockForkWorkflowArgs,
			resume_run_id: MockForkWorkflowRunId,
		},
		result: MockForkWorkflowStartedResult,
	},
	play: expand,
};

export const StillRunning: Story = {
	args: { result: MockForkWorkflowRunningResult },
	play: expand,
};

export const Stopped: Story = {
	args: { result: mockStoppedResult },
	play: expand,
};

export const Interrupted: Story = {
	args: {
		result: { ...MockForkWorkflowRunningResult, status: "interrupted" },
	},
	play: expand,
};

/** A run that was running when the tool returned shows its current state. */
export const StartedThenCompleted: Story = {
	args: {
		name: "run_workflow",
		args: mockRunWorkflowArgs,
		result: MockForkWorkflowStartedResult,
	},
	parameters: {
		reactRouter: reactRouterParameters({
			location: { pathParams: { agentId: MockForkWorkflowChatId } },
			routing: { path: "/agents/:agentId" },
		}),
	},
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "get").mockResolvedValue({
			data: MockChatWorkflowRunDetail,
		});
	},
	play: async (context) => {
		await within(context.canvasElement).findByRole("button", {
			name: /workflow completed/i,
		});
		await expand(context);
	},
};

export const Completed: Story = { play: expand };

export const CompletedResultExpanded: Story = {
	play: async (context) => {
		await expand(context);
		await userEvent.click(
			within(context.canvasElement).getByRole("button", { name: "Show more" }),
		);
	},
};

export const Failed: Story = {
	args: { result: MockForkWorkflowFailedResult },
	play: expand,
};

export const Waiting: Story = {
	args: { status: "running", result: undefined },
};

export const NotFound: Story = {
	args: {
		result:
			"The run is not found on this coderd replica, for example because coderd restarted.",
		isError: true,
	},
	play: expand,
};

export const StartFailed: Story = {
	args: {
		name: "run_workflow",
		args: { script: "def main(args):\n    return agent(" },
		result: "compile script: workflow.star:2:18: got end of file, want )",
		isError: true,
	},
	play: expand,
};
