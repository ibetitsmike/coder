import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { MockChatTodoList } from "#/testHelpers/forkChatTodos";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { Tool } from "./Tool";

const reviewerChatId = "6f1c2a4e-8b3d-4c5e-9f70-1a2b3c4d5e6f";
const docsChatId = "0d9e8f7a-6b5c-4d3e-8f21-0a1b2c3d4e5f";

const meta: Meta<typeof Tool> = {
	title: "pages/AgentsPage/ChatElements/tools/ForkSubagentFields",
	component: Tool,
	args: {
		status: "completed",
		isError: false,
		organizationId: "organization-id",
		mcpServers: [],
		subagentTitles: new Map(),
		subagentVariants: new Map(),
		shellToolDisplayMode: "auto",
		codeDiffDisplayMode: "auto",
	},
	parameters: {
		experiments: [
			"fork-chat-todos",
			"fork-nested-subagents",
			"fork-agent-definitions",
			"fork-subagent-isolation",
		],
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

const expand =
	(name: RegExp): Story["play"] =>
	async ({ canvasElement }) => {
		await userEvent.click(within(canvasElement).getByRole("button", { name }));
	};

export const ListAgents: Story = {
	args: {
		name: "list_agents",
		args: {},
		result: {
			agents: [
				{
					chat_id: reviewerChatId,
					title: "Review the diff",
					type: "reviewer",
					status: "running",
					todos: MockChatTodoList.todos,
				},
				{
					chat_id: docsChatId,
					title: "Rewrite the docs page",
					type: "general",
					status: "pending",
					waiting_for_capacity: true,
				},
			],
			total: 2,
		},
	},
	play: expand(/Listed 2 of 2 agents/),
};

// With the experiments off, the rows match upstream.
export const ListAgentsExperimentsOff: Story = {
	...ListAgents,
	parameters: { experiments: [] },
};

export const SpawnCustomType: Story = {
	args: {
		name: "spawn_agent",
		args: { type: "reviewer", title: "Review the diff", prompt: "Review it." },
		result: {
			chat_id: reviewerChatId,
			status: "pending",
			title: "Review the diff",
			type: "reviewer",
		},
	},
};

export const SpawnWorktree: Story = {
	args: {
		name: "spawn_agent",
		args: {
			type: "general",
			title: "Fix the flaky test",
			prompt: "Fix it.",
			isolation: "worktree",
		},
		result: {
			chat_id: reviewerChatId,
			status: "pending",
			title: "Fix the flaky test",
			type: "general",
			isolation: "worktree",
			branch: "agent/fix-the-flaky-test-6f1c2a4e",
			worktree_path:
				"/home/coder/.coder/worktrees/coder/agent-fix-the-flaky-test-6f1c2a4e",
		},
	},
};

export const SpawnWorktreeExperimentsOff: Story = {
	...SpawnWorktree,
	parameters: { experiments: [] },
};

export const SpawnWorkspace: Story = {
	args: {
		name: "spawn_agent",
		args: {
			type: "general",
			title: "Try the migration",
			prompt: "Run it.",
			isolation: "workspace",
		},
		result: {
			chat_id: docsChatId,
			status: "pending",
			title: "Try the migration",
			type: "general",
			isolation: "workspace",
			workspace_name: "dev-agent-0d9e",
		},
	},
};

export const SpawnWorkspaceError: Story = {
	args: {
		...SpawnWorkspace.args,
		result: {
			chat_id: docsChatId,
			status: "pending",
			title: "Try the migration",
			type: "general",
			isolation: "workspace",
			workspace_error: `get workspace of subagent ${docsChatId}: sql: no rows in result set`,
		},
	},
};

export const SpawnLimitError: Story = {
	args: {
		name: "spawn_agent",
		status: "error",
		isError: true,
		args: { type: "general", title: "Rewrite the docs page", prompt: "Go." },
		result: {
			error:
				"cannot create a child agent: this chat family has 3 live subagents and the maximum is 3",
		},
	},
};

export const WaitRecentToolCalls: Story = {
	args: {
		name: "wait_agent",
		args: { chat_id: reviewerChatId, include_tool_calls: 3 },
		result: {
			chat_id: reviewerChatId,
			title: "Review the diff",
			type: "general",
			status: "running",
			timed_out: true,
			tool_calls: [
				{
					tool_name: "read_file",
					args: { path: "site/src/pages/AgentsPage/AgentsPage.tsx" },
					result: { content: "export const AgentsPage" },
					created_at: "2026-11-10T09:00:00Z",
				},
				{
					tool_name: "execute",
					args: { command: "pnpm test" },
					result: { exit_code: 1 },
					is_error: true,
					created_at: "2026-11-10T09:01:00Z",
				},
				{
					tool_name: "todo_write",
					args: { todos: MockChatTodoList.todos },
					created_at: "2026-11-10T09:02:00Z",
				},
			],
		},
	},
	play: expand(/3 recent tool calls/),
};

export const WaitToolCallsError: Story = {
	args: {
		name: "wait_agent",
		args: { chat_id: reviewerChatId, include_tool_calls: 3 },
		result: {
			chat_id: reviewerChatId,
			title: "Review the diff",
			type: "general",
			status: "completed",
			report: "Looks good.",
			tool_calls_error: "read child chat messages: permission denied",
		},
	},
};

export const WaitWorktreeSummary: Story = {
	args: {
		name: "wait_agent",
		args: { chat_id: reviewerChatId },
		result: {
			chat_id: reviewerChatId,
			title: "Fix the flaky test",
			type: "general",
			status: "completed",
			report: "Fixed the race in the test setup.",
			isolation: "worktree",
			branch: "agent/fix-the-flaky-test-6f1c2a4e",
			worktree_path:
				"/home/coder/.coder/worktrees/coder/agent-fix-the-flaky-test-6f1c2a4e",
			diff_summary: {
				commits: 2,
				files_changed: 3,
				insertions: 41,
				deletions: 7,
				uncommitted_changes: 1,
			},
		},
	},
};

export const WaitWorktreeSummaryError: Story = {
	args: {
		name: "wait_agent",
		args: { chat_id: reviewerChatId },
		result: {
			chat_id: reviewerChatId,
			title: "Fix the flaky test",
			type: "general",
			status: "completed",
			report: "Fixed the race in the test setup.",
			isolation: "worktree",
			branch: "agent/fix-the-flaky-test-6f1c2a4e",
			worktree_path:
				"/home/coder/.coder/worktrees/coder/agent-fix-the-flaky-test-6f1c2a4e",
			diff_summary_error:
				"connect to workspace agent: context deadline exceeded",
		},
	},
};
