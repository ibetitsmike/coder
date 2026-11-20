import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import { ForkSettingsNavItems } from "./ForkSettingsNavItems";

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkSettingsNavItems", () => {
	it("navigates to the agent definitions page when the experiment is on", async () => {
		const user = userEvent.setup();
		vi.spyOn(API, "getExperiments").mockResolvedValue([
			"fork-agent-definitions",
		]);
		const { router } = renderWithAuth(
			<ForkSettingsNavItems settingsSection="general" state={null} />,
			{
				path: "/agents/settings",
				route: "/agents/settings",
				extraRoutes: [
					{ path: "/agents/settings/agent-definitions", element: <div /> },
				],
			},
		);

		await user.click(
			await screen.findByRole("link", { name: "Agent definitions" }),
		);

		await waitFor(() =>
			expect(router.state.location.pathname).toBe(
				"/agents/settings/agent-definitions",
			),
		);
	});

	it("navigates to the workflows page when only fork-workflows is on", async () => {
		const user = userEvent.setup();
		vi.spyOn(API, "getExperiments").mockResolvedValue(["fork-workflows"]);
		const { router } = renderWithAuth(
			<ForkSettingsNavItems settingsSection="general" state={null} />,
			{
				path: "/agents/settings",
				route: "/agents/settings",
				extraRoutes: [{ path: "/agents/settings/workflows", element: <div /> }],
			},
		);

		await user.click(await screen.findByRole("link", { name: "Workflows" }));

		await waitFor(() =>
			expect(router.state.location.pathname).toBe("/agents/settings/workflows"),
		);
	});

	it("renders no links while the fork experiments are off", async () => {
		vi.spyOn(API, "getExperiments").mockResolvedValue([]);
		renderWithAuth(
			<>
				<ForkSettingsNavItems settingsSection="general" state={null} />
				<span>Rendered</span>
			</>,
			{ path: "/agents/settings", route: "/agents/settings" },
		);

		await screen.findByText("Rendered");
		expect(screen.queryByRole("link")).toBeNull();
	});
});
