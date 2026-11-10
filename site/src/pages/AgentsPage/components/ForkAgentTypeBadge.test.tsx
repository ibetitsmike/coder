import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderComponent } from "#/testHelpers/renderHelpers";
import { ForkAgentTypeBadge } from "./ForkAgentTypeBadge";

describe("ForkAgentTypeBadge", () => {
	it("names a custom agent type", () => {
		renderComponent(<ForkAgentTypeBadge chat={{ agent_type: "reviewer" }} />);
		expect(screen.getByText("reviewer")).toHaveTextContent(
			"Agent type: reviewer",
		);
	});
});
