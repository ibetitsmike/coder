import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef } from "react";
import { QueryClientProvider } from "react-query";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import { mockApiError } from "#/testHelpers/entities";
import { MockChatWorkflowRunDetail } from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { ForkWorkflowStopButton } from "./ForkWorkflowStopButton";

const mockRunningRun: WorkflowRunDetail = {
	...MockChatWorkflowRunDetail,
	status: "running",
	result: undefined,
	finished_at: undefined,
};

const renderStopButton = (run: WorkflowRunDetail) => {
	const FallbackRefProbe = () => {
		const fallbackFocusRef = useRef<HTMLElement | null>(null);
		return (
			<ForkWorkflowStopButton run={run} fallbackFocusRef={fallbackFocusRef} />
		);
	};
	renderComponent(
		<QueryClientProvider client={createTestQueryClient()}>
			<FallbackRefProbe />
		</QueryClientProvider>,
	);
};

describe("ForkWorkflowStopButton", () => {
	it("stops the run only after the user confirms", async () => {
		const stopRun = vi
			.spyOn(forkWorkflowsApi, "stopChatWorkflowRun")
			.mockResolvedValue({ ...mockRunningRun, status: "stopped" });
		renderStopButton(mockRunningRun);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Stop workflow run" }));
		expect(stopRun).not.toHaveBeenCalled();

		await user.click(screen.getByRole("button", { name: "Stop run" }));

		await waitFor(() => expect(stopRun).toHaveBeenCalledTimes(1));
	});

	it("reports a conflict as the run having already ended", async () => {
		vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun").mockRejectedValue(
			mockApiError({ message: "The workflow run already ended.", status: 409 }),
		);
		const toastError = vi.spyOn(toast, "error");
		const toastInfo = vi.spyOn(toast, "info");
		renderStopButton(mockRunningRun);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Stop workflow run" }));
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		await waitFor(() =>
			expect(toastInfo).toHaveBeenCalledWith("The workflow run already ended."),
		);
		expect(toastError).not.toHaveBeenCalled();
	});

	it("reports a failed stop and keeps the run running", async () => {
		vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun").mockRejectedValue(
			mockApiError({ message: "The workflow run already ended." }),
		);
		const toastError = vi.spyOn(toast, "error");
		const toastInfo = vi.spyOn(toast, "info");
		renderStopButton(mockRunningRun);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Stop workflow run" }));
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		await waitFor(() =>
			expect(toastError).toHaveBeenCalledWith(
				"The workflow run already ended.",
				expect.anything(),
			),
		);
		expect(toastInfo).not.toHaveBeenCalled();
		// The run is still running, so the button stays.
		expect(
			screen.getByRole("button", { name: "Stop workflow run" }),
		).toBeDefined();
	});

	it("renders nothing for a run that is not running", () => {
		renderStopButton({ ...mockRunningRun, status: "completed" });
		expect(
			screen.queryByRole("button", { name: "Stop workflow run" }),
		).toBeNull();
	});
});
