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

/** A focus target the dialog falls back to when the button unmounts. */
const FocusTarget = ({
	fallbackFocusRef,
}: {
	fallbackFocusRef: React.RefObject<HTMLElement | null>;
}) => {
	return (
		<h3
			ref={(el) => {
				fallbackFocusRef.current = el;
			}}
			tabIndex={-1}
		>
			Run heading
		</h3>
	);
};

const renderStopButton = (run: WorkflowRunDetail) => {
	let currentRun = run;
	const FallbackRefProbe = () => {
		const fallbackFocusRef = useRef<HTMLElement | null>(null);
		return (
			<>
				<FocusTarget fallbackFocusRef={fallbackFocusRef} />
				<ForkWorkflowStopButton
					run={currentRun}
					fallbackFocusRef={fallbackFocusRef}
				/>
			</>
		);
	};
	renderComponent(
		<QueryClientProvider client={createTestQueryClient()}>
			<FallbackRefProbe />
		</QueryClientProvider>,
	);
	return (next: WorkflowRunDetail) => {
		currentRun = next;
	};
};

describe("ForkWorkflowStopButton", () => {
	it("stops the run only after the user confirms", async () => {
		const stopRun = vi
			.spyOn(forkWorkflowsApi, "stopChatWorkflowRun")
			.mockResolvedValue({ ...mockRunningRun, status: "stopped" });
		renderStopButton(mockRunningRun);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Stop" }));
		expect(stopRun).not.toHaveBeenCalled();

		await user.click(screen.getByRole("button", { name: "Stop run" }));

		await waitFor(() => expect(stopRun).toHaveBeenCalledTimes(1));
	});

	it("reports a failed stop and keeps the run running", async () => {
		vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun").mockRejectedValue(
			mockApiError({ message: "The workflow run already ended." }),
		);
		const toastError = vi.spyOn(toast, "error");
		renderStopButton(mockRunningRun);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Stop" }));
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		await waitFor(() =>
			expect(toastError).toHaveBeenCalledWith(
				"The workflow run already ended.",
				expect.anything(),
			),
		);
		// The run is still running, so the button stays.
		expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
	});

	it("renders nothing for a run that is not running", () => {
		renderStopButton({ ...mockRunningRun, status: "completed" });
		expect(
			screen.queryByRole("button", { name: "Stop" }),
		).not.toBeInTheDocument();
	});
});
