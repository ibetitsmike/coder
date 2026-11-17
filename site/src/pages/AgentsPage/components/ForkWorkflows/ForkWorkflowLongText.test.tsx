// This file is fork code: tests for the labeled long-text block
// (fork-workflows experiment).

import { render, screen } from "@testing-library/react";
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
			screen.getByRole("button", { name: "Show more", expanded: false }),
		).toBeInTheDocument();
		// The preview cuts the text, so the last line is not shown yet.
		expect(
			screen.queryByText(`line 12: ${"x".repeat(40)}`),
		).not.toBeInTheDocument();
	});

	it("keeps showing in full once the text grows past the threshold", () => {
		const { rerender } = render(
			<ForkWorkflowLongText label="Result" text={shortText} />,
		);
		// Short text shows in full with no control at all.
		expect(
			screen.queryByRole("button", { name: "Show less" }),
		).not.toBeInTheDocument();

		// Polling can grow the text past the threshold while it is on
		// screen; it must stay in full rather than collapse.
		rerender(<ForkWorkflowLongText label="Result" text={longText} />);
		expect(
			screen.getByRole("button", { name: "Show less", expanded: true }),
		).toBeInTheDocument();
		// The last line of the long text is in the document.
		const full = screen.getByRole("region", { name: "Full result" });
		expect(full).toHaveTextContent(`line 12: ${"x".repeat(40)}`);
	});
});
