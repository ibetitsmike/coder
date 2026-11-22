import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type QueryClient, QueryClientProvider } from "react-query";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkSavedWorkflowsApi from "#/api/forkSavedWorkflows";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import { getAuthorizationKey } from "#/api/queries/authCheck";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockChatOrganization,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockForkSavedWorkflow } from "#/testHelpers/forkSavedWorkflows";
import {
	MockChatWorkflowRunDetail,
	MockForkWorkflowChatId,
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
	options: {
		/** Leave the seeded answer stale, so opening the dialog refetches it. */
		stale?: boolean;
	} = {},
): ReturnType<typeof getAuthorizationKey> => {
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
	if (!options.stale) {
		// Without this the test client's gcTime of 0 would drop the seeded
		// answer before anything observes it.
		queryClient.setQueryDefaults(key, {
			staleTime: Number.POSITIVE_INFINITY,
			gcTime: Number.POSITIVE_INFINITY,
		});
	} else {
		// Keep the seed alive but stale: no staleTime means the dialog's
		// observer refetches it on mount, while the cached answer still
		// renders until that refetch settles.
		queryClient.setQueryDefaults(key, {
			gcTime: Number.POSITIVE_INFINITY,
		});
	}
	queryClient.setQueryData(key, { canCreateOrganizationSkill });
	return key;
};

// Each rerender stands in for a poll that returns the run. The chat and its
// update check are seeded, so the viewer may stop or resume.
const renderDetail = (
	run: WorkflowRunDetail,
	options: {
		canUpdateChat?: boolean;
		archived?: boolean;
		planMode?: boolean;
		/** Replaces the client renderDetail would create, so a test can
		    seed query data the view reads. */
		queryClient?: QueryClient;
	} = {},
) => {
	const queryClient = options.queryClient ?? createTestQueryClient();
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
					organizations: [MockDefaultOrganization, MockChatOrganization],
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
				...MockForkSavedWorkflow,
				id: "saved-workflow-new",
				name: "review-changed-files",
				organization_id: MockChat.organization_id,
				user_id: "me",
				created_by: "me",
				description: "",
				args_schema: undefined,
				phases: [],
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
				// The request builder trims the trailing newline the run stored.
				script: MockForkSavedWorkflow.script.trimEnd(),
			}),
		);
		expect(
			await screen.findByRole("link", { name: /Saved review-changed-files/ }),
		).toHaveAttribute(
			"href",
			`/agents/settings/workflows?org=${MockChatOrganization.name}`,
		);
		// Focus moves to the link that replaced the Save button.
		expect(
			screen.getByRole("link", { name: /Saved review-changed-files/ }),
		).toHaveFocus();
	});

	it("holds the submit until the organization permission resolves", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		// No create-permission seed: the authorization query stays pending.
		let resolvePermission:
			| ((value: { canCreateOrganizationSkill: boolean }) => void)
			| undefined;
		const authorizationSpy = vi
			.spyOn(API, "checkAuthorization")
			.mockImplementation(
				() =>
					new Promise((resolve) => {
						resolvePermission = resolve;
					}) as never,
			);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"createUserSavedWorkflow",
		).mockRejectedValue(new Error("must not be called"));
		renderDetail(MockChatWorkflowRunDetail);
		const user = userEvent.setup();

		try {
			await user.click(
				await screen.findByRole("button", { name: "Save workflow" }),
			);
			const dialog = screen.getByRole("dialog", { name: "Save workflow" });
			await within(dialog).findByLabelText("Name");
			const submit = within(dialog).getByRole("button", {
				name: "Save workflow",
			});
			// The pending answer blocks the save, so a pending query cannot
			// silently land in the personal tier.
			expect(submit).toBeDisabled();
			await user.click(submit);
			expect(
				forkSavedWorkflowsApi.createUserSavedWorkflow,
			).not.toHaveBeenCalled();

			resolvePermission?.({ canCreateOrganizationSkill: true });
			await within(dialog).findByLabelText("Organization");
			expect(submit).toBeEnabled();
		} finally {
			authorizationSpy.mockRestore();
		}
	});

	it("holds the submit while a cached permission answer refetches", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		// The seeded grant is left stale, so opening the dialog refetches it
		// while the cached answer still renders.
		const createPermissionKey = seedCreatePermission(queryClient, true, {
			stale: true,
		});
		// After the seed, every check request goes into this list, so the
		// test resolves each refetch separately.
		const refetchDeferreds: Array<
			(value: { canCreateOrganizationSkill: boolean }) => void
		> = [];
		const authorizationSpy = vi
			.spyOn(API, "checkAuthorization")
			.mockImplementation(
				() =>
					new Promise((resolve) => {
						refetchDeferreds.push(resolve);
					}) as never,
			);
		const create = vi
			.spyOn(forkSavedWorkflowsApi, "createUserSavedWorkflow")
			.mockRejectedValue(new Error("must not be called"));
		const createOrg = vi
			.spyOn(forkSavedWorkflowsApi, "createOrganizationSavedWorkflow")
			.mockRejectedValue(new Error("must not be called"));
		renderDetail(MockChatWorkflowRunDetail, { queryClient });
		const user = userEvent.setup();

		try {
			await user.click(
				await screen.findByRole("button", { name: "Save workflow" }),
			);
			const dialog = screen.getByRole("dialog", { name: "Save workflow" });
			const nameField = await within(dialog).findByLabelText("Name");
			const submit = within(dialog).getByRole("button", {
				name: "Save workflow",
			});
			// The in-flight refetch of the cached grant holds the submit, so a
			// grant revoked during the refetch cannot take a submission made
			// from the cached answer.
			await within(dialog).findByRole("status", {
				name: "Checking organization permission",
			});
			expect(submit).toBeDisabled();
			await user.click(submit);
			expect(create).not.toHaveBeenCalled();
			expect(createOrg).not.toHaveBeenCalled();

			refetchDeferreds[0]?.({ canCreateOrganizationSkill: true });
			const organizationRadio = await within(dialog).findByRole("radio", {
				name: "Organization",
			});
			expect(submit).toBeEnabled();

			// Invalidate the grant after selecting Organization: the refetch
			// runs while the selection survives, and this fresh answer denies it.
			await user.click(organizationRadio);
			expect(organizationRadio).toBeChecked();
			queryClient.invalidateQueries({ queryKey: createPermissionKey });
			await within(dialog).findByRole("status", {
				name: "Checking organization permission",
			});
			await user.click(submit);
			expect(create).not.toHaveBeenCalled();
			expect(createOrg).not.toHaveBeenCalled();
			refetchDeferreds[1]?.({ canCreateOrganizationSkill: false });
			// The stale selection must not survive the denial as the
			// submitted tier.
			await waitFor(() =>
				expect(
					within(dialog).queryByRole("radio", { name: "Organization" }),
				).toBeNull(),
			);
			await user.clear(nameField);
			await user.type(nameField, "review-changed-files");
			await user.click(submit);
			await waitFor(() => expect(create).toHaveBeenCalledOnce());
			expect(createOrg).not.toHaveBeenCalled();
		} finally {
			authorizationSpy.mockRestore();
			create.mockRestore();
			createOrg.mockRestore();
		}
	});

	it("shows the checking loader while a cached denial refetches", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		// A cached denial hides the tier choice already, so the refetch
		// must still show what holds the submit.
		seedCreatePermission(queryClient, false, {
			stale: true,
		});
		let resolveRefetch:
			| ((value: { canCreateOrganizationSkill: boolean }) => void)
			| undefined;
		const authorizationSpy = vi
			.spyOn(API, "checkAuthorization")
			.mockImplementation(
				() =>
					new Promise((resolve) => {
						resolveRefetch = resolve;
					}) as never,
			);
		const create = vi
			.spyOn(forkSavedWorkflowsApi, "createUserSavedWorkflow")
			.mockResolvedValue({
				...MockForkSavedWorkflow,
				id: "saved-workflow-new",
				name: "review-changed-files",
				organization_id: MockChat.organization_id,
				user_id: "me",
				created_by: "me",
				description: "",
				args_schema: undefined,
				phases: [],
			});
		const createOrg = vi
			.spyOn(forkSavedWorkflowsApi, "createOrganizationSavedWorkflow")
			.mockRejectedValue(new Error("must not be called"));
		renderDetail(MockChatWorkflowRunDetail, { queryClient });
		const user = userEvent.setup();

		try {
			await user.click(
				await screen.findByRole("button", { name: "Save workflow" }),
			);
			const dialog = screen.getByRole("dialog", { name: "Save workflow" });
			const submit = within(dialog).getByRole("button", {
				name: "Save workflow",
			});
			await within(dialog).findByRole("status", {
				name: "Checking organization permission",
			});
			expect(submit).toBeDisabled();
			// The loader alone is not the point: a submission made from the
			// cached denial must not reach either create API.
			await user.click(submit);
			expect(create).not.toHaveBeenCalled();
			expect(createOrg).not.toHaveBeenCalled();

			resolveRefetch?.({ canCreateOrganizationSkill: false });
			await waitFor(() => expect(submit).toBeEnabled());
			expect(within(dialog).queryByRole("status")).toBeNull();
			// After the fresh denial settles, saving still goes to the
			// personal tier only.
			await user.click(submit);
			await waitFor(() => expect(create).toHaveBeenCalledOnce());
			expect(createOrg).not.toHaveBeenCalled();
		} finally {
			authorizationSpy.mockRestore();
			create.mockRestore();
			createOrg.mockRestore();
		}
	});

	it("keeps Personal usable when the organization permission check fails", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		const authorizationSpy = vi
			.spyOn(API, "checkAuthorization")
			.mockRejectedValue(new Error("check failed") as never);
		const create = vi
			.spyOn(forkSavedWorkflowsApi, "createUserSavedWorkflow")
			.mockResolvedValue({
				...MockForkSavedWorkflow,
				id: "saved-workflow-new",
				name: "review-changed-files",
				organization_id: MockChat.organization_id,
				user_id: "me",
				created_by: "me",
				description: "",
				args_schema: undefined,
				phases: [],
			});
		renderDetail(MockChatWorkflowRunDetail);
		const user = userEvent.setup();

		try {
			await user.click(
				await screen.findByRole("button", { name: "Save workflow" }),
			);
			const dialog = screen.getByRole("dialog", { name: "Save workflow" });
			// The failed check shows its error in place of the tier choice, and
			// the personal tier, which needs no organization permission, stays
			// usable instead of waiting on a check that already failed.
			await within(dialog).findByText("check failed");
			// A failed check settles the tier like a denied one: personal only.
			expect(within(dialog).queryByLabelText("Organization")).toBeNull();
			const submit = within(dialog).getByRole("button", {
				name: "Save workflow",
			});
			expect(submit).toBeEnabled();
			await user.click(submit);
			await waitFor(() => expect(create).toHaveBeenCalledOnce());
		} finally {
			authorizationSpy.mockRestore();
			create.mockRestore();
		}
	});

	it("trims the name before creating the workflow", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		seedCreatePermission(queryClient, true);
		const create = vi
			.spyOn(forkSavedWorkflowsApi, "createUserSavedWorkflow")
			.mockResolvedValue({
				...MockForkSavedWorkflow,
				id: "saved-workflow-new",
				name: "review-changed-files",
				organization_id: MockChat.organization_id,
				user_id: "me",
				created_by: "me",
				description: "",
				args_schema: undefined,
				phases: [],
			});
		renderDetail(MockChatWorkflowRunDetail);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: "Save workflow" }),
		);
		const dialog = screen.getByRole("dialog", { name: "Save workflow" });
		const nameField = within(dialog).getByLabelText("Name");
		await user.clear(nameField);
		// Whitespace around an otherwise valid name validates, so the
		// request must send the trimmed value the validation accepted.
		await user.type(nameField, "  review-changed-files  ");
		await user.click(
			within(dialog).getByRole("button", { name: "Save workflow" }),
		);

		await waitFor(() => expect(create).toHaveBeenCalledOnce());
		expect(create).toHaveBeenCalledWith(
			MockChat.organization_id,
			expect.objectContaining({ name: "review-changed-files" }),
		);
	});

	it("shows only the personal tier without organization skill create permission", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		seedCreatePermission(queryClient, false);
		const create = vi
			.spyOn(forkSavedWorkflowsApi, "createUserSavedWorkflow")
			.mockRejectedValue(new Error("must not be called"));
		renderDetail(MockChatWorkflowRunDetail, { queryClient });
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: "Save workflow" }),
		);
		const dialog = screen.getByRole("dialog", { name: "Save workflow" });
		// The seeded denial reaches the view, so the whole tier choice is
		// gone and the dialog saves straight into the personal tier.
		expect(within(dialog).queryByLabelText("Organization")).toBeNull();
		const submit = within(dialog).getByRole("button", {
			name: "Save workflow",
		});
		expect(submit).toBeEnabled();
		await user.click(submit);
		await waitFor(() => expect(create).toHaveBeenCalledOnce());
	});
});
