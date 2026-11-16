import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import {
	MockChatWorkflowRunDetail,
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

// Each rerender stands in for a poll that returns the run. The chat and its
// update check are seeded, so the viewer may stop or resume.
const renderDetail = (
	run: WorkflowRunDetail,
	options: { canUpdateChat?: boolean } = {},
) => {
	const queryClient = createTestQueryClient();
	seedChatWriteAccess(queryClient, run.chat_id, options.canUpdateChat ?? true);
	const view = (current: WorkflowRunDetail) => (
		<QueryClientProvider client={queryClient}>
			<MemoryRouter>
				<ForkWorkflowRunDetailView
					run={current}
					error={null}
					onBack={vi.fn()}
				/>
			</MemoryRouter>
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
				dispatched.push(event as CustomEvent);
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
});
