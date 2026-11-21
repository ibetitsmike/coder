import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, spyOn, userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { API } from "#/api/api";
import { getAuthorizationKey } from "#/api/queries/authCheck";
import { chatEntityKey } from "#/api/queries/chats";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockChatOrganization,
	MockDefaultOrganization,
} from "#/testHelpers/entities";
import { MockForkSavedWorkflow } from "#/testHelpers/forkSavedWorkflows";
import {
	MockChatWorkflowRunDetail,
	MockForkWorkflowNow,
} from "#/testHelpers/forkWorkflows";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { ForkWorkflowRunDetailView } from "./ForkWorkflowRunDetail";

const mockRunningRunDetail: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "running",
	// The view names an untitled run after its first step.
	title: "",
	result: undefined,
	logs: [],
	finished_at: undefined,
	steps: MockChatWorkflowRunDetail.steps.map((step, index) =>
		index === 0
			? step
			: {
					...step,
					status: "running",
					value: undefined,
					finished_at: undefined,
				},
	),
};

// A running run shows its args, the log lines so far and each step's
// report, before the result exists.
const mockRunningRunDetailWithProgress: WorkflowRunDetail = {
	...mockRunningRunDetail,
	logs: ["reviewing coderd/chats.go", "reviewing coderd/users.go"],
	steps: MockChatWorkflowRunDetail.steps.map((step, index) => ({
		...step,
		status: index === 2 ? "running" : "reported",
		finished_at: index === 2 ? undefined : step.finished_at,
		value: index === 2 ? undefined : step.value,
	})),
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
					value: undefined,
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
			: {
					...step,
					status: "failed",
					error: "stopped by user",
					value: undefined,
				},
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
		value: index === 2 ? undefined : step.value,
	})),
};

const baseQueries = [
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
];

const meta: Meta<typeof ForkWorkflowRunDetailView> = {
	title: "pages/AgentsPage/RightPanel/ForkWorkflowRunDetail",
	component: ForkWorkflowRunDetailView,
	args: { run: MockChatWorkflowRunDetail, error: null, onBack: fn() },
	parameters: {
		experiments: ["fork-workflows"],
		reactRouter: reactRouterParameters({ routing: { path: "/" } }),
		// The stories spy on Date.now. Storybook's actions addon logs every
		// mock call, and its log line calls Date.now, so the spy recurses.
		actions: { disable: true },
		queries: baseQueries,
		organizations: [MockDefaultOrganization, MockChatOrganization],
	},
	beforeEach: () => {
		// Named "spy" so Storybook's actions addon skips logging it: its log
		// line calls Date.now, which would recurse into the mock.
		spyOn(Date, "now").mockName("spy").mockReturnValue(MockForkWorkflowNow);
		spyOn(API, "checkAuthorization").mockResolvedValue({
			canUpdateChat: true,
			canCreateOrganizationSkill: true,
		});
	},
	decorators: [
		(Story) => (
			<div className="w-[360px] font-sans">
				<Story />
			</div>
		),
		withDashboardProvider,
	],
};

export default meta;
type Story = StoryObj<typeof ForkWorkflowRunDetailView>;

export const Completed: Story = {};

/** A completed run whose Save button opened the save dialog. */
export const CompletedSaveDialog: Story = {
	parameters: {
		queries: [
			...baseQueries,
			{
				key: getAuthorizationKey({
					checks: {
						canCreateOrganizationSkill: {
							object: {
								resource_type: "organization_skill",
								organization_id: MockChat.organization_id,
							},
							action: "create",
						},
					},
				}),
				data: { canCreateOrganizationSkill: true },
			},
		],
	},
	play: async ({ canvas }) => {
		await userEvent.click(
			canvas.getByRole("button", { name: "Save workflow" }),
		);
		await screen.findByRole("dialog", { name: "Save workflow" });
	},
};

/**
 * The save dialog of a viewer who may not create organization workflows: the
 * tier choice is gone and only Personal remains.
 */
export const CompletedSaveDialogPersonalOnly: Story = {
	parameters: {
		queries: [
			...baseQueries,
			{
				key: getAuthorizationKey({
					checks: {
						canCreateOrganizationSkill: {
							object: {
								resource_type: "organization_skill",
								organization_id: MockChat.organization_id,
							},
							action: "create",
						},
					},
				}),
				data: { canCreateOrganizationSkill: false },
			},
		],
	},
	play: async ({ canvas }) => {
		await userEvent.click(
			canvas.getByRole("button", { name: "Save workflow" }),
		);
		await screen.findByRole("dialog", { name: "Save workflow" });
	},
};

/**
 * The save dialog while the organization permission check is still loading:
 * the tier choice shows a loader and the submit waits for the answer.
 */
export const CompletedSaveDialogPermissionPending: Story = {
	parameters: {
		queries: baseQueries,
	},
	beforeEach: () => {
		// The permission check never answers, so the pending state stays.
		spyOn(API, "checkAuthorization").mockReturnValue(new Promise(() => {}));
	},
	play: async ({ canvas }) => {
		await userEvent.click(
			canvas.getByRole("button", { name: "Save workflow" }),
		);
		await screen.findByRole("status", {
			name: "Checking organization permission",
		});
	},
};

/**
 * The save dialog when the organization permission check fails: the error
 * shows in place of the tier choice and Personal stays usable.
 */
export const CompletedSaveDialogPermissionFailed: Story = {
	parameters: {
		queries: baseQueries,
	},
	beforeEach: () => {
		spyOn(API, "checkAuthorization").mockRejectedValue(
			new Error("check failed") as never,
		);
	},
	play: async ({ canvas }) => {
		await userEvent.click(
			canvas.getByRole("button", { name: "Save workflow" }),
		);
		await screen.findByText("check failed");
	},
};

// A name at the 128-byte limit, for the story whose saved link has to
// truncate visually without losing its accessible name.
const mockLongSavedName = `${"review-changed-files-".repeat(5)}reviewed`.slice(
	0,
	128,
);

/**
 * A completed run after the run's script was saved under a name at the
 * 128-byte limit: the saved link truncates instead of overflowing.
 */
export const CompletedSavedLongName: Story = {
	parameters: {
		queries: [
			...baseQueries,
			{
				key: getAuthorizationKey({
					checks: {
						canCreateOrganizationSkill: {
							object: {
								resource_type: "organization_skill",
								organization_id: MockChat.organization_id,
							},
							action: "create",
						},
					},
				}),
				data: { canCreateOrganizationSkill: true },
			},
		],
	},
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "post").mockResolvedValue({
			data: {
				...MockForkSavedWorkflow,
				id: "saved-workflow-new",
				name: mockLongSavedName,
				organization_id: MockChat.organization_id,
				user_id: "me",
				created_by: "me",
				description: "",
				args_schema: undefined,
				phases: [],
			},
		});
	},
	play: async ({ canvas }) => {
		await userEvent.click(
			canvas.getByRole("button", { name: "Save workflow" }),
		);
		const dialog = await screen.findByRole("dialog", {
			name: "Save workflow",
		});
		const nameField = within(dialog).getByLabelText("Name");
		await userEvent.clear(nameField);
		await userEvent.type(nameField, mockLongSavedName);
		await userEvent.click(
			within(dialog).getByRole("button", { name: "Save workflow" }),
		);
		// Awaiting the link sets the screenshot state; the accessible-name
		// assertion lives in ForkWorkflowRunDetail.test.tsx.
		await screen.findByRole("link", { name: `Saved ${mockLongSavedName}` });
	},
};

/** A completed run after the run's script was saved. */
export const CompletedSaved: Story = {
	parameters: {
		queries: [
			...baseQueries,
			{
				key: getAuthorizationKey({
					checks: {
						canCreateOrganizationSkill: {
							object: {
								resource_type: "organization_skill",
								organization_id: MockChat.organization_id,
							},
							action: "create",
						},
					},
				}),
				data: { canCreateOrganizationSkill: true },
			},
		],
	},
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "post").mockResolvedValue({
			data: {
				...MockForkSavedWorkflow,
				id: "saved-workflow-new",
				name: "review-changed-files",
				organization_id: MockChat.organization_id,
				user_id: "me",
				created_by: "me",
				description: "",
				args_schema: undefined,
				phases: [],
			},
		});
	},
	play: async ({ canvas }) => {
		await userEvent.click(
			canvas.getByRole("button", { name: "Save workflow" }),
		);
		const dialog = await screen.findByRole("dialog", {
			name: "Save workflow",
		});
		await userEvent.click(
			within(dialog).getByRole("button", { name: "Save workflow" }),
		);
		// Awaiting the link sets the screenshot state; the behavior
		// assertions live in ForkWorkflowRunDetail.test.tsx.
		await screen.findByRole("link", { name: /Saved review-changed-files/ });
	},
};

export const Running: Story = {
	args: { run: mockRunningRunDetail },
};

/**
 * A running run shows its args, the log lines so far and the value of each
 * step that has reported, while the result is still absent.
 */
export const RunningWithProgress: Story = {
	args: { run: mockRunningRunDetailWithProgress },
};

/**
 * A running run whose script prints nothing yet: the detail says where the
 * logs will appear instead of an empty block.
 */
export const RunningWithoutLogs: Story = {
	args: { run: { ...mockRunningRunDetail, logs: [], steps: [] } },
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

/** The rail shows declared phases the run has not reached yet. */
export const RunningWithPhases: Story = {
	args: { run: mockPhasedRunDetail },
};

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
