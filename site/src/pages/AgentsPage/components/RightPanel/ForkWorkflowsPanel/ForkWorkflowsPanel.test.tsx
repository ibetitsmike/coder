import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
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

const renderPanel = () => {
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
	return router;
};

describe("ForkWorkflowsPanel", () => {
	it("stops a running run after the user confirms", async () => {
		let run: WorkflowRunDetail = {
			...MockChatWorkflowRunDetail,
			status: "running",
			result: undefined,
			finished_at: undefined,
		};
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockResolvedValue([
			{ ...MockChatWorkflowRun, status: "running" },
		]);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockImplementation(
			async () => run,
		);
		const stopRun = vi
			.spyOn(forkWorkflowsApi, "stopChatWorkflowRun")
			.mockImplementation(async () => {
				run = { ...run, status: "stopped", error: "stopped by user" };
				return run;
			});
		renderPanel();
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		await user.click(await screen.findByRole("button", { name: "Stop" }));
		expect(stopRun).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		expect(stopRun).toHaveBeenCalledWith(
			MockForkWorkflowChatId,
			MockChatWorkflowRunDetail.id,
		);
		expect(await screen.findByText("Stopped")).toBeInTheDocument();
		expect(
			screen.queryByRole("button", { name: "Stop" }),
		).not.toBeInTheDocument();
	});

	it("opens a run, then a step's child chat", async () => {
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockResolvedValue([
			MockChatWorkflowRun,
		]);
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockResolvedValue(MockChatWorkflowRunDetail);
		const router = renderPanel();
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
