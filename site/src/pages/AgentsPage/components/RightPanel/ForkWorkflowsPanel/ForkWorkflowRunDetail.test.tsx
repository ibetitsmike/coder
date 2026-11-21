import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type QueryClient, QueryClientProvider } from "react-query";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import * as forkSavedWorkflowsApi from "#/api/forkSavedWorkflows";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import { getAuthorizationKey } from "#/api/queries/authCheck";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import {
	MockChatWorkflowRunDetail,
	MockForkWorkflowChatId,
	MockForkWorkflowScript,
	seedChatWriteAccess,
} from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { ForkWorkflowRunDetailView } from "./ForkWorkflowRunDetail";

const mockRunningRunDetail: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "running",
	result: undefined,
	finished_at: undefined,
};

const seedCreatePermission = (
	queryClient: QueryClient,
	canCreateOrganizationSkill: boolean,
) => {
	const key = getAuthorizationKey({
		checks: {
			canCreateOrganizationSkill: {
				object: {
					resource_type: "organization_skill",
					organization_id: MockChat.organization_id,
				},
				action: "create",
			},
		},
	});
	queryClient.setQueryDefaults(key, {
		staleTime: Number.POSITIVE_INFINITY,
		gcTime: Number.POSITIVE_INFINITY,
	});
	queryClient.setQueryData(key, { canCreateOrganizationSkill });
};

// Each rerender stands in for a poll that returns the run. The chat and its
// update check are seeded, so the viewer may stop or resume.
const renderDetail = (
	run: WorkflowRunDetail,
	options: {
		canUpdateChat?: boolean;
		archived?: boolean;
		planMode?: boolean;
	} = {},
) => {
	const queryClient = createTestQueryClient();
	seedChatWriteAccess(queryClient, run.chat_id, options.canUpdateChat ?? true, {
		archived: options.archived,
		planMode: options.planMode,
	});
	const view = (current: WorkflowRunDetail) => (
		<QueryClientProvider client={queryClient}>
			<DashboardContext.Provider
				value={{
					entitlements: MockEntitlements,
					experiments: ["fork-workflows"],
					appearance: MockAppearanceConfig,
					buildInfo: MockBuildInfo,
					organizations: [MockDefaultOrganization],
					showOrganizations: false,
					canViewOrganizationSettings: false,
				}}
			>
				<MemoryRouter>
					<ForkWorkflowRunDetailView
						run={current}
						error={null}
						onBack={vi.fn()}
					/>
				</MemoryRouter>
			</DashboardContext.Provider>
		</QueryClientProvider>
	);
	const { rerender } = renderComponent(view(run));
	return (next: WorkflowRunDetail) => rerender(view(next));
};

const runHeading = () =>
	screen.getByRole("heading", { name: /review changed files/i });

describe("ForkWorkflowRunDetail", () => {
	it("closes the stop dialog when the run ends on its own", async () => {
		const poll = renderDetail(mockRunningRunDetail);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Stop workflow run" }));
		poll({ ...mockRunningRunDetail, status: "completed" });

		await waitFor(() => expect(runHeading()).toHaveFocus());
		// A resumed run does not reopen the dialog, which would take focus.
		poll(mockRunningRunDetail);
		expect(runHeading()).toHaveFocus();
	});

	it("keeps the stop dialog open until the stop request settles", async () => {
		let resolveStop: (run: WorkflowRunDetail) => void = () => {};
		vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun").mockReturnValue(
			new Promise((resolve) => {
				resolveStop = resolve;
			}),
		);
		const poll = renderDetail(mockRunningRunDetail);
		const user = userEvent.setup();
		const mockStoppedRunDetail: WorkflowRunDetail = {
			...mockRunningRunDetail,
			status: "stopped",
			error: "stopped by user",
		};

		await user.click(screen.getByRole("button", { name: "Stop workflow run" }));
		await user.click(screen.getByRole("button", { name: "Stop run" }));
		await user.keyboard("{Escape}");
		// The stop response updates the run before the request settles.
		poll(mockStoppedRunDetail);
		resolveStop(mockStoppedRunDetail);

		await waitFor(() => expect(runHeading()).toHaveFocus());
	});

	it("offers Resume for an interrupted run", async () => {
		renderDetail({
			...mockRunningRunDetail,
			status: "interrupted",
		});

		const dispatched: CustomEvent[] = [];
		const dispatchSpy = vi
			.spyOn(window, "dispatchEvent")
			.mockImplementation((event: Event) => {
				if (event instanceof CustomEvent) {
					dispatched.push(event);
				}
				return true;
			});
		const user = userEvent.setup();
		try {
			await user.click(
				await screen.findByRole("button", { name: "Resume run" }),
			);
		} finally {
			dispatchSpy.mockRestore();
		}
		expect(dispatched).toHaveLength(1);
		expect(dispatched[0].type).toBe("fork-workflow-resume");
		expect(dispatched[0].detail).toEqual({
			chatId: MockChatWorkflowRunDetail.chat_id,
			prompt: `Resume workflow run ${MockChatWorkflowRunDetail.id}`,
		});
	});

	it("hides the controls without update access to the chat", async () => {
		renderDetail(mockRunningRunDetail, { canUpdateChat: false });

		await screen.findByRole("heading", { name: /review changed files/i });
		expect(
			screen.queryByRole("button", { name: "Stop workflow run" }),
		).not.toBeInTheDocument();
		expect(
			screen.queryByRole("button", { name: "Resume run" }),
		).not.toBeInTheDocument();
	});

	it("hides the controls for an archived chat", async () => {
		renderDetail(
			{ ...mockRunningRunDetail, status: "interrupted" },
			{ archived: true },
		);

		await screen.findByRole("heading", { name: /review changed files/i });
		expect(
			screen.queryByRole("button", { name: "Stop workflow run" }),
		).not.toBeInTheDocument();
		expect(
			screen.queryByRole("button", { name: "Resume run" }),
		).not.toBeInTheDocument();
	});

	it("hides Resume while the chat is in plan mode", async () => {
		renderDetail(
			{ ...mockRunningRunDetail, status: "interrupted" },
			{ planMode: true },
		);

		await screen.findByRole("heading", { name: /review changed files/i });
		expect(
			screen.queryByRole("button", { name: "Resume run" }),
		).not.toBeInTheDocument();
	});

	it("keeps Stop while the chat is in plan mode", async () => {
		renderDetail(mockRunningRunDetail, { planMode: true });

		await screen.findByRole("button", { name: "Stop workflow run" });
	});

	it("saves the run's script as a personal workflow from the panel", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		seedCreatePermission(queryClient, true);
		const create = vi
			.spyOn(forkSavedWorkflowsApi, "createUserSavedWorkflow")
			.mockResolvedValue({
				...MockChatWorkflowRunDetail,
				id: "saved-workflow-new",
				name: "review-changed-files",
				description: "",
				user_id: "me",
				created_by: "me",
				organization_id: MockChat.organization_id,
				args_schema: undefined,
				phases: [],
				script: MockForkWorkflowScript,
				created_at: "2026-11-21T00:00:00Z",
				updated_at: "2026-11-21T00:00:00Z",
			});
		renderDetail(MockChatWorkflowRunDetail);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: "Save workflow" }),
		);
		const dialog = screen.getByRole("dialog", { name: "Save workflow" });
		expect(within(dialog).getByLabelText("Name")).toHaveValue(
			"review-changed-files",
		);
		await user.click(
			within(dialog).getByRole("button", { name: "Save workflow" }),
		);

		await waitFor(() => expect(create).toHaveBeenCalledOnce());
		expect(create).toHaveBeenCalledWith(
			MockChat.organization_id,
			expect.objectContaining({
				name: "review-changed-files",
				script: MockForkWorkflowScript,
			}),
		);
		expect(
			await screen.findByRole("link", { name: /Saved review-changed-files/ }),
		).toHaveAttribute("href", "/agents/settings/workflows");
	});

	it("hides the save action without organization skill create permission", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		seedCreatePermission(queryClient, false);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"createUserSavedWorkflow",
		).mockRejectedValue(new Error("must not be called"));
		renderDetail(MockChatWorkflowRunDetail);

		await screen.findByRole("button", { name: "Save workflow" });
	});
});
