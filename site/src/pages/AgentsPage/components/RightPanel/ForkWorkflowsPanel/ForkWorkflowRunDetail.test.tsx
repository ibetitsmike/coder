import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { MockChatWorkflowRunDetail } from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { ForkWorkflowRunDetailView } from "./ForkWorkflowRunDetail";

const runningRun: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "running",
	result: undefined,
	finished_at: undefined,
};

// Each rerender stands in for a poll that returns the run.
const renderDetail = (run: WorkflowRunDetail) => {
	const queryClient = createTestQueryClient();
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
		const stopRun = vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun");
		const poll = renderDetail(runningRun);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Stop" }));
		poll({ ...runningRun, status: "completed" });

		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
		expect(runHeading()).toHaveFocus();
		// A resumed run does not reopen the dialog.
		poll(runningRun);
		expect(screen.queryByRole("dialog")).toBeNull();
		expect(stopRun).not.toHaveBeenCalled();
	});

	it("keeps the stop dialog open until the stop request settles", async () => {
		let resolveStop: (run: WorkflowRunDetail) => void = () => {};
		vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun").mockReturnValue(
			new Promise((resolve) => {
				resolveStop = resolve;
			}),
		);
		const poll = renderDetail(runningRun);
		const user = userEvent.setup();
		const stoppedRun: WorkflowRunDetail = {
			...runningRun,
			status: "stopped",
			error: "stopped by user",
		};

		await user.click(screen.getByRole("button", { name: "Stop" }));
		await user.click(screen.getByRole("button", { name: "Stop run" }));
		await user.keyboard("{Escape}");
		// The stop response updates the run before the request settles.
		poll(stoppedRun);
		expect(screen.getByRole("dialog")).toBeInTheDocument();

		resolveStop(stoppedRun);
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
		expect(runHeading()).toHaveFocus();
	});
});
