import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import type { Experiment } from "#/api/typesGenerated";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockUserMember,
} from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import ForkAgentDefinitionsPage from "./ForkAgentDefinitionsPage";

const renderPage = (experiments: Experiment[] = ["fork-agent-definitions"]) => {
	vi.spyOn(API, "getExperiments").mockResolvedValue(experiments);
	vi.spyOn(API, "getAuthenticatedUser").mockResolvedValue({
		...MockUserMember,
		organization_ids: [MockOrganization2.id, MockDefaultOrganization.id],
	});
	vi.spyOn(API, "getOrganizations").mockResolvedValue([
		MockOrganization2,
		MockDefaultOrganization,
	]);
	const getDefinitions = vi
		.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinitions")
		.mockResolvedValue([MockForkAgentDefinitionMetadata]);
	const { router } = renderWithAuth(<ForkAgentDefinitionsPage />, {
		path: "/agents/settings/agent-definitions",
		route: "/agents/settings/agent-definitions",
		extraRoutes: [{ path: "/agents/settings", element: <div /> }],
	});
	return { getDefinitions, router };
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkAgentDefinitionsPage", () => {
	it("redirects to the settings index without listing when the experiment is off", async () => {
		const { getDefinitions, router } = renderPage([]);

		await waitFor(() =>
			expect(router.state.location.pathname).toBe("/agents/settings"),
		);
		expect(getDefinitions).not.toHaveBeenCalled();
	});

	it("lists the default organization first and follows the picker", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();

		await waitFor(() =>
			expect(getDefinitions).toHaveBeenCalledWith(MockDefaultOrganization.id),
		);
		expect(getDefinitions).not.toHaveBeenCalledWith(MockOrganization2.id);

		await user.click(
			await screen.findByRole("combobox", {
				name: `Organization ${MockDefaultOrganization.display_name}`,
			}),
		);
		await user.click(
			screen.getByRole("option", {
				name: new RegExp(MockOrganization2.display_name),
			}),
		);
		await waitFor(() =>
			expect(getDefinitions).toHaveBeenLastCalledWith(MockOrganization2.id),
		);
	});
});
