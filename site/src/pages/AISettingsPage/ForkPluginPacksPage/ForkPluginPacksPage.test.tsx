import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkPluginPacksApi from "#/api/forkPluginPacks";
import {
	MockNoPermissions,
	MockOrganization,
	MockOrganization2,
	MockUserOwner,
	mockApiError,
} from "#/testHelpers/entities";
import { MockForkPluginPack } from "#/testHelpers/forkPluginPacks";
import { renderWithRouter } from "#/testHelpers/renderHelpers";
import ForkPluginPacksPage from "./ForkPluginPacksPage";

vi.mock("#/hooks/useAuthenticated", () => ({
	useAuthenticated: () => ({
		user: MockUserOwner,
		permissions: { ...MockNoPermissions, viewAnyOrganizationSkills: true },
	}),
}));
const dashboard = vi.hoisted((): { experiments: string[] } => ({
	experiments: [],
}));
vi.mock("#/modules/dashboard/useDashboard", () => ({
	useDashboard: () => ({
		experiments: dashboard.experiments,
		organizations: [MockOrganization, MockOrganization2],
	}),
}));

const path = "/ai/settings/plugin-packs";

const renderPage = (search: string) =>
	renderWithRouter(
		createMemoryRouter(
			[
				{ path, element: <ForkPluginPacksPage /> },
				{ path: "/ai/settings", element: null },
			],
			{ initialEntries: [`${path}${search}`] },
		),
	);

beforeEach(() => {
	dashboard.experiments = ["fork-plugin-packs"];
	vi.spyOn(API, "checkAuthorization").mockImplementation(async ({ checks }) =>
		Object.fromEntries(Object.keys(checks).map((key) => [key, true])),
	);
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
	]);
});

afterEach(() => {
	vi.restoreAllMocks();
});

it("lists the packs of the organization the URL names", async () => {
	renderPage(`?org=${MockOrganization2.name}`);

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledWith(
			MockOrganization2.id,
		),
	);
	expect(forkPluginPacksApi.getPluginPacks).not.toHaveBeenCalledWith(
		MockOrganization.id,
	);
});

it("requests the packs again when Retry is clicked after a failed load", async () => {
	vi.mocked(forkPluginPacksApi.getPluginPacks)
		.mockRejectedValueOnce(
			mockApiError({ message: "Failed to list plugin packs." }),
		)
		.mockResolvedValueOnce([MockForkPluginPack]);
	renderPage(`?org=${MockOrganization2.name}`);

	await userEvent.click(await screen.findByRole("button", { name: "Retry" }));

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2),
	);
});

it("switches the organization through the picker", async () => {
	const user = userEvent.setup();
	const { router } = renderPage("");
	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledWith(
			MockOrganization.id,
		),
	);

	await user.click(
		await screen.findByRole("combobox", { name: /^Organization/ }),
	);
	await user.click(
		await screen.findByRole("option", { name: /My Organization 2/ }),
	);

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledWith(
			MockOrganization2.id,
		),
	);
	expect(new URLSearchParams(router.state.location.search).get("org")).toBe(
		MockOrganization2.name,
	);
});

it("redirects to AI settings while the experiment is off", async () => {
	dashboard.experiments = [];
	const { router } = renderPage(`?org=${MockOrganization.name}`);

	await waitFor(() =>
		expect(router.state.location.pathname).toBe("/ai/settings"),
	);
	expect(API.checkAuthorization).not.toHaveBeenCalled();
	expect(forkPluginPacksApi.getPluginPacks).not.toHaveBeenCalled();
});
