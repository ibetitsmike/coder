import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
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
		<QueryClientProvider client={createTestQueryClient()}>
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

describe("ForkWorkflowTool", () => {
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

		expect(
			await screen.findByRole("button", { name: /workflow completed/i }),
		).toBeInTheDocument();
		expect(getRun).toHaveBeenCalledWith(
			MockForkWorkflowChatId,
			MockForkWorkflowStartedResult.run_id,
			expect.anything(),
		);
	});
});
