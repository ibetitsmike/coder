import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockChatWorkflowRun,
	MockChatWorkflowRunDetail,
} from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { generateUUID } from "#/utils/random";
import { ForkWorkflowsPanel } from "./ForkWorkflowsPanel";

// The panel remembers the open run per chat, so each test uses its own chat.
const renderPanel = (chatId: string) => {
	const chatPath = `/agents/${chatId}`;
	const router = createMemoryRouter(
		[
			{
				path: chatPath,
				element: <ForkWorkflowsPanel chatId={chatId} />,
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
		const chatId = generateUUID();
		let run: WorkflowRunDetail = {
			...MockChatWorkflowRunDetail,
			chat_id: chatId,
			status: "running",
			result: undefined,
			finished_at: undefined,
		};
		const getRuns = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRuns")
			.mockResolvedValue([{ ...MockChatWorkflowRun, status: "running" }]);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockImplementation(
			async () => run,
		);
		const stopRun = vi
			.spyOn(forkWorkflowsApi, "stopChatWorkflowRun")
			.mockImplementation(async () => {
				run = { ...run, status: "stopped", error: "stopped by user" };
				return run;
			});
		renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		await user.click(await screen.findByRole("button", { name: "Stop" }));
		expect(stopRun).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		expect(stopRun).toHaveBeenCalledWith(chatId, MockChatWorkflowRunDetail.id);
		await waitFor(() => expect(getRuns).toHaveBeenCalledTimes(2));
		expect(
			screen.queryByRole("button", { name: "Stop" }),
		).not.toBeInTheDocument();
		expect(
			screen.getByRole("heading", { name: /review changed files/i }),
		).toHaveFocus();
	});

	it("reports a failed stop and refreshes the runs", async () => {
		const chatId = generateUUID();
		const runningRun: WorkflowRunDetail = {
			...MockChatWorkflowRunDetail,
			chat_id: chatId,
			status: "running",
			result: undefined,
			finished_at: undefined,
		};
		const getRuns = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRuns")
			.mockResolvedValue([{ ...MockChatWorkflowRun, status: "running" }]);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			runningRun,
		);
		vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun").mockRejectedValue(
			mockApiError({ message: "The workflow run already ended." }),
		);
		const toastError = vi.spyOn(toast, "error");
		renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		await user.click(await screen.findByRole("button", { name: "Stop" }));
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		await waitFor(() =>
			expect(toastError).toHaveBeenCalledWith(
				"The workflow run already ended.",
				expect.anything(),
			),
		);
		// The run may have ended on its own, so the list refreshes anyway.
		await waitFor(() => expect(getRuns).toHaveBeenCalledTimes(2));
	});

	it("opens a run, then a step's child chat, and back again", async () => {
		const chatId = generateUUID();
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockResolvedValue([
			MockChatWorkflowRun,
		]);
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockResolvedValue(MockChatWorkflowRunDetail);
		const router = renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		expect(getRun).toHaveBeenCalledWith(
			chatId,
			MockChatWorkflowRun.id,
			expect.anything(),
		);
		await user.click(
			await screen.findByRole("link", { name: /coderd\/users\.go/ }),
		);
		expect(router.state.location.pathname).toBe(
			`/agents/${MockChatWorkflowRunDetail.steps[1].id}`,
		);

		// Browser back remounts the panel on the run that was open.
		await router.navigate(-1);
		await screen.findByRole("button", { name: "All runs" });
	});
});
