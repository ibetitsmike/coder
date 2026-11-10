import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router";
import { expect, it, vi } from "vitest";
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
vi.mock("#/modules/dashboard/useDashboard", () => ({
	useDashboard: () => ({
		experiments: ["fork-agent-definitions"],
		organizations: [MockOrganization, MockOrganization2],
	}),
}));

it("toggles a definition in the organization the URL names", async () => {
	const user = userEvent.setup();
	vi.spyOn(API, "checkAuthorization").mockImplementation(async ({ checks }) =>
		Object.fromEntries(Object.keys(checks).map((key) => [key, true])),
	);
	const list = vi
		.spyOn(forkAgentDefinitionsApi, "getOrganizationAgentDefinitions")
		.mockResolvedValue([MockForkAgentDefinitionMetadata]);
	const update = vi
		.spyOn(forkAgentDefinitionsApi, "updateOrganizationAgentDefinition")
		.mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });
	const path = "/ai/settings/agent-definitions";
	renderWithRouter(
		createMemoryRouter(
			[{ path, element: <ForkOrganizationAgentDefinitionsPage /> }],
			{ initialEntries: [`${path}?org=${MockOrganization2.name}`] },
		),
	);

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
