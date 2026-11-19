// This file is fork code: tests for the labeled long-text block
// (fork-workflows experiment).

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ForkWorkflowLongText } from "./ForkWorkflowLongText";

const shortText = "the handler writes to a nil map";

const longText = Array.from(
	{ length: 13 },
	(_, index) => `line ${index}: ${"x".repeat(40)}`,
).join("\n");

describe("ForkWorkflowLongText", () => {
	it("previews text over the threshold and offers Show more", () => {
		render(<ForkWorkflowLongText label="Result" text={longText} />);
		expect(
			screen.getByRole("button", { expanded: false }),
		).toHaveAccessibleName("Show more of Result");
	});

	it("keeps showing in full once the text grows past the threshold", () => {
		const { rerender } = render(
			<ForkWorkflowLongText label="Result" text={shortText} />,
		);

		// Polling can grow the text past the threshold while it is on
		// screen; it must stay in full rather than collapse.
		rerender(<ForkWorkflowLongText label="Result" text={longText} />);
		expect(screen.getByRole("button", { expanded: true })).toHaveAccessibleName(
			"Show less of Result",
		);
	});

	it("keeps a live log region stable when it grows past the threshold", () => {
		const { rerender } = render(
			<ForkWorkflowLongText label="Logs" text={shortText} isLive />,
		);
		const logs = screen.getByRole("log", { name: "Logs" });
		const pre = logs.querySelector("pre");
		expect(pre).not.toBeNull();

		// The run grows its log past the preview threshold while the
		// region stays live. The pre must survive the growth, so a screen
		// reader announces only the appended text, not the whole log.
		rerender(<ForkWorkflowLongText label="Logs" text={longText} isLive />);
		expect(screen.getByRole("log", { name: "Logs" })).toBe(logs);
		expect(logs).toHaveTextContent("line 12");
		// The expansion control never lives inside the live region.
		expect(within(logs).queryByRole("button")).toBeNull();
		// The grown log is still scrollable without remounting a subtree.
		expect(logs.querySelector("pre")).toHaveClass("max-h-96");
	});

	it("names each toggle by its block so two blocks stay apart", async () => {
		const user = userEvent.setup();
		render(
			<>
				<ForkWorkflowLongText label="Result" text={longText} />
				<ForkWorkflowLongText label="Logs" text={longText} />
			</>,
		);

		await user.click(
			screen.getByRole("button", { name: "Show more of Result" }),
		);
		screen.getByRole("button", { name: "Show less of Result", expanded: true });
		screen.getByRole("button", { name: "Show more of Logs", expanded: false });
	});
});
