import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type QueryClient, QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import { chatEntityKey } from "#/api/queries/chats";
import { forkPanelResyncInterval } from "#/api/queries/forkPanelResync";
import { chatWorkflowRunKey } from "#/api/queries/forkWorkflows";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
	mockApiError,
} from "#/testHelpers/entities";
import {
	MockChatWorkflowRunDetail,
	MockForkWorkflowChatId,
	MockForkWorkflowCompletedResult,
	MockForkWorkflowRunId,
	MockForkWorkflowRunningResult,
	MockForkWorkflowStartedResult,
	seedChatWriteAccess,
} from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { Tool } from "./Tool";

const renderWorkflowTool = (
	name: "run_workflow" | "await_workflow",
	result: unknown,
	queryClient: QueryClient = createTestQueryClient(),
) => {
	const router = createMemoryRouter(
		[
			{
				path: "/agents/:agentId",
				element: (
					<Tool
						name={name}
						status="completed"
						args={{ run_id: MockForkWorkflowCompletedResult.run_id }}
						result={JSON.stringify(result)}
						isError={false}
						organizationId="organization-id"
						mcpServers={[]}
						subagentTitles={new Map()}
						subagentVariants={new Map()}
						shellToolDisplayMode="auto"
						codeDiffDisplayMode="auto"
					/>
				),
			},
		],
		{ initialEntries: [`/agents/${MockForkWorkflowChatId}`] },
	);
	renderComponent(
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
				<RouterProvider router={router} />
			</DashboardContext.Provider>
		</QueryClientProvider>,
	);
	return router;
};

const mockServerError = {
	...mockApiError({ message: "Internal error." }),
	status: 500,
};

const mockForbiddenError = {
	...mockApiError({ message: "Forbidden." }),
	status: 403,
};

const mockRunningDetail: typeof MockChatWorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "running",
	result: undefined,
	finished_at: undefined,
};

/** The settled run the stop mutation recorded, which the panel's cache holds. */
const stopMutationData = (queryClient: QueryClient) =>
	queryClient
		.getMutationCache()
		.getAll()
		.find((mutation) => mutation.state.status === "success")?.state.data as
		| typeof MockChatWorkflowRunDetail
		| undefined;

describe("ForkWorkflowTool", () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	it("opens a step's child chat from the step list", async () => {
		const router = renderWorkflowTool(
			"await_workflow",
			MockForkWorkflowCompletedResult,
		);
		const user = userEvent.setup();
		await user.click(
			screen.getByRole("button", { name: /workflow completed/i }),
		);
		await user.click(screen.getByRole("link", { name: /coderd\/users\.go/ }));
		expect(router.state.location.pathname).toBe(
			`/agents/${MockForkWorkflowCompletedResult.steps[1].chat_id}`,
		);
	});

	it("shows the run's current status when the tool saw it running", async () => {
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockResolvedValue(MockChatWorkflowRunDetail);
		renderWorkflowTool("run_workflow", MockForkWorkflowStartedResult);

		// The header label comes from the run API, not the tool result.
		await screen.findByRole("button", { name: /workflow completed/i });
		expect(getRun).toHaveBeenCalledWith(
			MockForkWorkflowChatId,
			MockForkWorkflowStartedResult.run_id,
			expect.anything(),
		);
	});

	it("polls a resumed run past a cached interrupted run", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const queryClient = createTestQueryClient();
		queryClient.setQueryData(
			chatWorkflowRunKey(
				MockForkWorkflowChatId,
				MockForkWorkflowStartedResult.run_id,
			),
			{ ...MockChatWorkflowRunDetail, status: "interrupted" },
			{ updatedAt: Date.now() - 60_000 },
		);
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockRejectedValue(mockServerError);
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);

		await waitFor(() => expect(getRun).toHaveBeenCalledTimes(1));
		await vi.advanceTimersByTimeAsync(forkPanelResyncInterval);
		expect(getRun).toHaveBeenCalledTimes(2);
	});

	it("polls the run again after a transient error", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockRejectedValue(mockServerError);
		renderWorkflowTool("run_workflow", MockForkWorkflowStartedResult);

		await waitFor(() => expect(getRun).toHaveBeenCalledTimes(1));
		await vi.advanceTimersByTimeAsync(forkPanelResyncInterval);
		expect(getRun).toHaveBeenCalledTimes(2);
	});

	it("stops polling when the viewer cannot read the run", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockRejectedValue(mockForbiddenError);
		renderWorkflowTool("run_workflow", MockForkWorkflowStartedResult);

		await waitFor(() => expect(getRun).toHaveBeenCalledTimes(1));
		await vi.advanceTimersByTimeAsync(forkPanelResyncInterval * 2);
		expect(getRun).toHaveBeenCalledTimes(1);
	});

	it("stops a running run from the live card", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockResolvedValue(mockRunningDetail);
		const stopRun = vi
			.spyOn(forkWorkflowsApi, "stopChatWorkflowRun")
			.mockResolvedValue({
				...mockRunningDetail,
				status: "stopped",
				error: "stopped by user",
			});
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);
		const user = userEvent.setup();
		await user.click(
			await screen.findByRole("button", { name: /^started workflow/i }),
		);

		await user.click(
			await screen.findByRole("button", { name: "Stop workflow run" }),
		);
		expect(stopRun).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		expect(stopRun).toHaveBeenCalledWith(
			MockForkWorkflowChatId,
			MockForkWorkflowRunningResult.run_id,
		);
		// The stop writes the settled run through the mutation, then the
		// settled invalidation refetches it. The refetch here still reports
		// running, so the settled mutation data is what proves the stopped
		// state was recorded and reached the cache the panel reads.
		await waitFor(() =>
			expect(stopMutationData(queryClient)?.status).toBe("stopped"),
		);
		expect(stopMutationData(queryClient)).toMatchObject({
			error: "stopped by user",
		});
		await waitFor(() =>
			expect(
				queryClient.getQueryState(
					chatWorkflowRunKey(
						MockForkWorkflowChatId,
						MockForkWorkflowRunningResult.run_id,
					),
				)?.isInvalidated,
			).toBe(false),
		);
		expect(getRun.mock.calls.length).toBeGreaterThanOrEqual(2);
	});

	it("hides the card controls while the update check is pending", async () => {
		const queryClient = createTestQueryClient();
		// The chat is seeded but its update check never resolves.
		const seed = (key: readonly unknown[], data: unknown) => {
			queryClient.setQueryDefaults(key, {
				staleTime: Number.POSITIVE_INFINITY,
				gcTime: Number.POSITIVE_INFINITY,
			});
			queryClient.setQueryData(key, data);
		};
		seed(chatEntityKey(MockForkWorkflowChatId), {
			...MockChat,
			id: MockForkWorkflowChatId,
		});
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			mockRunningDetail,
		);
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);
		const user = userEvent.setup();
		await user.click(
			await screen.findByRole("button", { name: /^started workflow/i }),
		);

		await screen.findByText(`Run ${MockForkWorkflowRunningResult.run_id}`);
		expect(
			screen.queryByRole("button", { name: "Stop workflow run" }),
		).not.toBeInTheDocument();
	});

	it("hides the card controls without update access to the chat", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, false);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			mockRunningDetail,
		);
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);
		const user = userEvent.setup();
		await user.click(
			await screen.findByRole("button", { name: /^started workflow/i }),
		);

		await screen.findByText(`Run ${MockForkWorkflowRunningResult.run_id}`);
		expect(
			screen.queryByRole("button", { name: "Stop workflow run" }),
		).not.toBeInTheDocument();
	});

	it("offers Resume on the live card for an interrupted run", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		const interruptedDetail: typeof MockChatWorkflowRunDetail = {
			...mockRunningDetail,
			status: "interrupted",
		};
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			interruptedDetail,
		);
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);
		const user = userEvent.setup();
		await user.click(
			await screen.findByRole("button", { name: /^started workflow/i }),
		);

		// The resume request carries the run and its chat; the composer
		// listener applies it in ForkWorkflowResumePrefill's tests.
		const dispatched: CustomEvent[] = [];
		const dispatchSpy = vi
			.spyOn(window, "dispatchEvent")
			.mockImplementation((event: Event) => {
				if (event instanceof CustomEvent) {
					dispatched.push(event);
				}
				return true;
			});
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
			chatId: MockForkWorkflowChatId,
			prompt: `Resume workflow run ${MockForkWorkflowRunningResult.run_id}`,
		});
	});

	it("hides Resume from an interrupted run of a chat in plan mode", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true, {
			planMode: true,
		});
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue({
			...mockRunningDetail,
			status: "interrupted",
		});
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);
		const user = userEvent.setup();
		await user.click(
			await screen.findByRole("button", { name: /^started workflow/i }),
		);

		await screen.findByText(`Run ${MockForkWorkflowRunId}`);
		expect(
			screen.queryByRole("button", { name: "Resume run" }),
		).not.toBeInTheDocument();
	});

	it("keeps Stop on a live card of a chat in plan mode", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true, {
			planMode: true,
		});
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			mockRunningDetail,
		);
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);
		const user = userEvent.setup();
		await user.click(
			await screen.findByRole("button", { name: /^started workflow/i }),
		);

		await screen.findByRole("button", { name: "Stop workflow run" });
	});

	it("reports a failed stop from the live card", async () => {
		const queryClient = createTestQueryClient();
		seedChatWriteAccess(queryClient, MockForkWorkflowChatId, true);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			mockRunningDetail,
		);
		vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun").mockRejectedValue(
			mockApiError({ message: "The workflow run already ended." }),
		);
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);
		const user = userEvent.setup();
		await user.click(
			await screen.findByRole("button", { name: /^started workflow/i }),
		);

		await user.click(
			await screen.findByRole("button", { name: "Stop workflow run" }),
		);
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		// The confirm dialog closes once the request settles.
		await waitFor(() =>
			expect(
				screen.queryByRole("button", { name: "Stop run" }),
			).not.toBeInTheDocument(),
		);
	});
});
