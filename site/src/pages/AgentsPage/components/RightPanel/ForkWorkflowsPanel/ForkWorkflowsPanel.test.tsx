import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import {
	MockChatWorkflowRun,
	MockChatWorkflowRunDetail,
	MockForkWorkflowChatId,
} from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { ForkWorkflowsPanel } from "./ForkWorkflowsPanel";

describe("ForkWorkflowsPanel", () => {
	it("opens a run, then a step's child chat", async () => {
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockResolvedValue([
			MockChatWorkflowRun,
		]);
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockResolvedValue(MockChatWorkflowRunDetail);
		const chatPath = `/agents/${MockForkWorkflowChatId}`;
		const router = createMemoryRouter(
			[
				{
					path: chatPath,
					element: <ForkWorkflowsPanel chatId={MockForkWorkflowChatId} />,
				},
				{ path: "/agents/:chatId", element: null },
			],
			{ initialEntries: [chatPath] },
		);
		renderComponent(
			<QueryClientProvider client={createTestQueryClient()}>
				<RouterProvider router={router} />
			</QueryClientProvider>,
		);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		expect(getRun).toHaveBeenCalledWith(
			MockForkWorkflowChatId,
			MockChatWorkflowRun.id,
			expect.anything(),
		);
		await user.click(
			await screen.findByRole("link", { name: /coderd\/users\.go/ }),
		);
		expect(router.state.location.pathname).toBe(
			`/agents/${MockChatWorkflowRunDetail.steps[1].id}`,
		);
	});
});
