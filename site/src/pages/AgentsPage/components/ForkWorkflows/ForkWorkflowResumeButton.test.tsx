import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderComponent } from "#/testHelpers/renderHelpers";
import { ForkWorkflowResumeButton } from "./ForkWorkflowResumeButton";
import {
	FORK_WORKFLOW_RESUME_EVENT,
	forkWorkflowResumePrompt,
} from "./forkWorkflowResume";

describe("ForkWorkflowResumeButton", () => {
	it("asks the matching chat's composer to prefill a resume request", async () => {
		const dispatchEvent = vi.spyOn(window, "dispatchEvent");
		renderComponent(<ForkWorkflowResumeButton runId="run-1" chatId="chat-1" />);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Resume run" }));

		expect(dispatchEvent).toHaveBeenCalledTimes(1);
		const event = dispatchEvent.mock.calls[0][0] as CustomEvent;
		expect(event.type).toBe(FORK_WORKFLOW_RESUME_EVENT);
		expect(event.detail).toEqual({
			chatId: "chat-1",
			prompt: forkWorkflowResumePrompt("run-1"),
		});
	});

	it("does not prefill twice for the same run", async () => {
		const dispatchEvent = vi.spyOn(window, "dispatchEvent");
		renderComponent(<ForkWorkflowResumeButton runId="run-2" chatId="chat-1" />);
		const user = userEvent.setup();
		const button = screen.getByRole("button", { name: "Resume run" });

		await user.click(button);
		await user.click(button);

		expect(dispatchEvent).toHaveBeenCalledTimes(1);
	});

	it("uses the custom label as the accessible name", async () => {
		const dispatchEvent = vi.spyOn(window, "dispatchEvent");
		renderComponent(
			<ForkWorkflowResumeButton
				runId="run-3"
				chatId="chat-1"
				label="Resume workflow"
			/>,
		);
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: "Resume workflow" }));

		expect(dispatchEvent).toHaveBeenCalledTimes(1);
	});
});
