import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router";
import { beforeEach, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import {
	MockNoPermissions,
	MockOrganization,
	MockOrganization2,
	MockUserOwner,
} from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { renderWithRouter } from "#/testHelpers/renderHelpers";
import ForkOrganizationAgentDefinitionsPage from "./ForkOrganizationAgentDefinitionsPage";

vi.mock("#/hooks/useAuthenticated", () => ({
	useAuthenticated: () => ({
		user: MockUserOwner,
		permissions: { ...MockNoPermissions, viewAnyOrganizationSkills: true },
	}),
}));
const dashboard = vi.hoisted(() => ({ experiments: [] as string[] }));
vi.mock("#/modules/dashboard/useDashboard", () => ({
	useDashboard: () => ({
		experiments: dashboard.experiments,
		organizations: [MockOrganization, MockOrganization2],
	}),
}));

const path = "/ai/settings/agent-definitions";

const renderPage = (search: string) => {
	const router = createMemoryRouter(
		[
			{ path, element: <ForkOrganizationAgentDefinitionsPage /> },
			{ path: "/ai/settings", element: null },
		],
		{ initialEntries: [`${path}${search}`] },
	);
	renderWithRouter(router);
	return router;
};

beforeEach(() => {
	dashboard.experiments = ["fork-agent-definitions"];
	vi.spyOn(API, "checkAuthorization").mockImplementation(async ({ checks }) =>
		Object.fromEntries(Object.keys(checks).map((key) => [key, true])),
	);
});

it("toggles a definition in the organization the URL names", async () => {
	const user = userEvent.setup();
	const list = vi
		.spyOn(forkAgentDefinitionsApi, "getOrganizationAgentDefinitions")
		.mockResolvedValue([MockForkAgentDefinitionMetadata]);
	const update = vi
		.spyOn(forkAgentDefinitionsApi, "updateOrganizationAgentDefinition")
		.mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("switch", {
			name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
		}),
	);

	await waitFor(() =>
		expect(update).toHaveBeenCalledWith(
			MockOrganization2.id,
			MockForkAgentDefinitionMetadata.name,
			{ enabled: !MockForkAgentDefinitionMetadata.enabled },
		),
	);
	expect(list).toHaveBeenCalledWith(MockOrganization2.id);
	expect(list).not.toHaveBeenCalledWith(MockOrganization.id);
});

it("switches the organization through the picker", async () => {
	const user = userEvent.setup();
	const list = vi
		.spyOn(forkAgentDefinitionsApi, "getOrganizationAgentDefinitions")
		.mockResolvedValue([MockForkAgentDefinitionMetadata]);
	const router = renderPage("");
	await waitFor(() => expect(list).toHaveBeenCalledWith(MockOrganization.id));

	await user.click(await screen.findByRole("combobox"));
	await user.click(
		await screen.findByRole("option", { name: /My Organization 2/ }),
	);

	await waitFor(() => expect(list).toHaveBeenCalledWith(MockOrganization2.id));
	expect(new URLSearchParams(router.state.location.search).get("org")).toBe(
		MockOrganization2.name,
	);
});

it("redirects to AI settings while the experiment is off", async () => {
	dashboard.experiments = [];
	const list = vi.spyOn(
		forkAgentDefinitionsApi,
		"getOrganizationAgentDefinitions",
	);
	const router = renderPage(`?org=${MockOrganization.name}`);

	await waitFor(() =>
		expect(router.state.location.pathname).toBe("/ai/settings"),
	);
	expect(list).not.toHaveBeenCalled();
});
