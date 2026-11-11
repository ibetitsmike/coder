import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import { ForkSidebarNavItems } from "./ForkSidebarNavItems";

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkSidebarNavItems", () => {
	it("opens the agent definitions page and keeps the page to return to", async () => {
		const user = userEvent.setup();
		vi.spyOn(API, "getExperiments").mockResolvedValue([
			"fork-agent-definitions",
		]);
		const { router } = renderWithAuth(
			<ForkSidebarNavItems state={{ from: "/agents/chat-1" }} />,
			{
				path: "/agents",
				route: "/agents",
				extraRoutes: [
					{ path: "/agents/settings/agent-definitions", element: <div /> },
				],
			},
		);

		await user.click(await screen.findByRole("link", { name: "Agents" }));

		await waitFor(() =>
			expect(router.state.location.pathname).toBe(
				"/agents/settings/agent-definitions",
			),
		);
		expect(router.state.location.state).toEqual({ from: "/agents/chat-1" });
	});
});
