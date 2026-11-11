import { waitFor } from "@testing-library/react";
import { createMemoryRouter } from "react-router";
import { beforeEach, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkPluginPacksApi from "#/api/forkPluginPacks";
import {
	MockNoPermissions,
	MockOrganization,
	MockOrganization2,
	MockUserOwner,
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
vi.mock("#/modules/dashboard/useDashboard", () => ({
	useDashboard: () => ({
		experiments: ["fork-plugin-packs"],
		organizations: [MockOrganization, MockOrganization2],
	}),
}));

const path = "/ai/settings/plugin-packs";

const renderPage = () =>
	renderWithRouter(
		createMemoryRouter([{ path, element: <ForkPluginPacksPage /> }], {
			initialEntries: [`${path}?org=${MockOrganization2.name}`],
		}),
	);

beforeEach(() => {
	vi.spyOn(API, "checkAuthorization").mockImplementation(async ({ checks }) =>
		Object.fromEntries(Object.keys(checks).map((key) => [key, true])),
	);
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
	]);
});

it("lists the packs of the organization the URL names", async () => {
	renderPage();

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledWith(
			MockOrganization2.id,
		),
	);
	expect(forkPluginPacksApi.getPluginPacks).not.toHaveBeenCalledWith(
		MockOrganization.id,
	);
});
