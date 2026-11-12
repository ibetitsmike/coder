import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type QueryClient, QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import { chatWorkflowRun } from "#/api/queries/forkWorkflows";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
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
	MockForkWorkflowStartedResult,
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

const mockStatusError = (status: number) => ({
	...mockApiError({ message: `Status ${status}.` }),
	status,
});

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

	it("shows a resumed run as running over a cached interrupted run", async () => {
		const queryClient = createTestQueryClient();
		queryClient.setQueryData(
			chatWorkflowRun(
				MockForkWorkflowChatId,
				MockForkWorkflowStartedResult.run_id,
			).queryKey,
			{ ...MockChatWorkflowRunDetail, status: "interrupted" },
			{ updatedAt: Date.now() - 60_000 },
		);
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockRejectedValue(mockStatusError(500));
		renderWorkflowTool(
			"run_workflow",
			MockForkWorkflowStartedResult,
			queryClient,
		);

		await waitFor(() => expect(getRun).toHaveBeenCalled());
		await screen.findByRole("button", { name: /started workflow/i });
	});

	it("polls the run again after a transient error", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockRejectedValueOnce(mockStatusError(500))
			.mockResolvedValue(MockChatWorkflowRunDetail);
		renderWorkflowTool("run_workflow", MockForkWorkflowStartedResult);

		await waitFor(() => expect(getRun).toHaveBeenCalledTimes(1));
		await vi.advanceTimersByTimeAsync(3_000);
		await screen.findByRole("button", { name: /workflow completed/i });
	});

	it("stops polling when the viewer cannot read the run", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockRejectedValue(mockStatusError(403));
		renderWorkflowTool("run_workflow", MockForkWorkflowStartedResult);

		await waitFor(() => expect(getRun).toHaveBeenCalledTimes(1));
		await vi.advanceTimersByTimeAsync(10_000);
		expect(getRun).toHaveBeenCalledTimes(1);
	});
});
