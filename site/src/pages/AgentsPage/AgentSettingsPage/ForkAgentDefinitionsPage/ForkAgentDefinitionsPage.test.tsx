import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockUserMember,
} from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import ForkAgentDefinitionsPage from "./ForkAgentDefinitionsPage";

const renderPage = () => {
	vi.spyOn(API, "getExperiments").mockResolvedValue(["fork-agent-definitions"]);
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
	renderWithAuth(<ForkAgentDefinitionsPage />);
	return { getDefinitions };
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkAgentDefinitionsPage", () => {
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

	it("disables a definition in the selected organization", async () => {
		const user = userEvent.setup();
		renderPage();
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateUserAgentDefinition")
			.mockResolvedValue({
				...MockForkAgentDefinitionMetadata,
				enabled: false,
				content: "",
			});

		await user.click(
			await screen.findByRole("switch", {
				name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);

		await waitFor(() =>
			expect(update).toHaveBeenCalledWith(
				MockDefaultOrganization.id,
				MockForkAgentDefinitionMetadata.name,
				{ enabled: false },
			),
		);
	});

	it("deletes a definition only after confirmation", async () => {
		const user = userEvent.setup();
		renderPage();
		const remove = vi
			.spyOn(forkAgentDefinitionsApi, "deleteUserAgentDefinition")
			.mockResolvedValue();

		await user.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
		await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));
		expect(remove).not.toHaveBeenCalled();
		await user.click(await screen.findByRole("button", { name: "Delete" }));

		await waitFor(() =>
			expect(remove).toHaveBeenCalledWith(
				MockDefaultOrganization.id,
				MockForkAgentDefinitionMetadata.name,
			),
		);
	});
});
