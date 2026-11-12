import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	MockForkWorkflowArgs,
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

const runWorkflowArgs = {
	script: MockForkWorkflowScript,
	args: MockForkWorkflowArgs,
};

export const Started: Story = {
	args: {
		name: "run_workflow",
		args: runWorkflowArgs,
		result: MockForkWorkflowStartedResult,
	},
	play: expand,
};

export const Starting: Story = {
	args: {
		name: "run_workflow",
		status: "running",
		args: runWorkflowArgs,
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
