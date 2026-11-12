import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it } from "vitest";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockForkWorkflowCompletedResult } from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { Tool } from "./Tool";

const renderAwaitWorkflow = () => {
	const router = createMemoryRouter(
		[
			{
				path: "/agents/root-chat",
				element: (
					<Tool
						name="await_workflow"
						status="completed"
						args={{ run_id: MockForkWorkflowCompletedResult.run_id }}
						result={JSON.stringify(MockForkWorkflowCompletedResult)}
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
			{ path: "/agents/:chatId", element: null },
		],
		{ initialEntries: ["/agents/root-chat"] },
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
		const router = renderAwaitWorkflow();
		const user = userEvent.setup();
		await user.click(
			screen.getByRole("button", { name: /workflow completed/i }),
		);
		await user.click(screen.getByRole("link", { name: /coderd\/users\.go/ }));
		expect(router.state.location.pathname).toBe(
			`/agents/${MockForkWorkflowCompletedResult.steps[1].chat_id}`,
		);
	});
});
