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
		const event = dispatchEvent.mock.calls[0][0];
		if (!(event instanceof CustomEvent)) {
			return expect.fail("the dispatched event is not a CustomEvent");
		}
		expect(event.type).toBe(FORK_WORKFLOW_RESUME_EVENT);
		expect(event.detail).toEqual({
			chatId: "chat-1",
			prompt: forkWorkflowResumePrompt("run-1"),
		});
	});

	it("prefills again for a run whose chat is not the open chat", async () => {
		// No listener of another chat consumes the event, so every click
		// dispatches: nothing is stuck swallowing later clicks.
		const dispatchEvent = vi.spyOn(window, "dispatchEvent");
		renderComponent(
			<ForkWorkflowResumeButton runId="run-2" chatId="other-chat" />,
		);
		const user = userEvent.setup();
		const button = screen.getByRole("button", { name: "Resume run" });

		await user.click(button);
		await user.click(button);

		expect(dispatchEvent).toHaveBeenCalledTimes(2);
	});
});
