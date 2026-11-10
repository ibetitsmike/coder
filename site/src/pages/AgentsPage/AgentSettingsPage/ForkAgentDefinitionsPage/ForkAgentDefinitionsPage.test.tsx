import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import type {
	Experiment,
	ForkAgentDefinition,
	Organization,
} from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockUserMember,
	mockApiError,
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

	it("reports a failed toggle with the server message", async () => {
		const user = userEvent.setup();
		renderPage();
		const toastError = vi.spyOn(toast, "error");
		vi.spyOn(
			forkAgentDefinitionsApi,
			"updateUserAgentDefinition",
		).mockRejectedValue(
			mockApiError({ message: "Permission denied.", detail: "read-only" }),
		);

		await user.click(
			await screen.findByRole("switch", {
				name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);

		await waitFor(() =>
			expect(toastError).toHaveBeenCalledWith("Permission denied.", {
				description: "read-only",
			}),
		);
	});

	it("deletes a definition only after confirmation", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		const toastSuccess = vi.spyOn(toast, "success");
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
		getDefinitions.mockResolvedValue([]);
		await user.click(await screen.findByRole("button", { name: "Delete" }));

		await waitFor(() =>
			expect(remove).toHaveBeenCalledWith(
				MockDefaultOrganization.id,
				MockForkAgentDefinitionMetadata.name,
			),
		);
		await waitFor(() =>
			expect(toastSuccess).toHaveBeenCalledWith("Agent definition deleted."),
		);
		// The deleted row was the only button left to take focus back.
		await waitFor(() =>
			expect(
				screen.getByRole("table", { name: "Agent definitions" }),
			).toHaveFocus(),
		);
	});

	it("keeps the delete dialog open through Escape so a failed delete can be retried", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		let rejectDelete: (error: unknown) => void = () => {};
		const remove = vi
			.spyOn(forkAgentDefinitionsApi, "deleteUserAgentDefinition")
			.mockImplementationOnce(
				() =>
					new Promise((_resolve, reject) => {
						rejectDelete = reject;
					}),
			)
			.mockResolvedValue();

		await user.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
		await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));
		await user.click(await screen.findByRole("button", { name: "Delete" }));
		await user.keyboard("{Escape}");
		rejectDelete(mockApiError({ message: "Upstream timeout." }));
		// A failed delete may still have removed the definition.
		await waitFor(() => expect(getDefinitions).toHaveBeenCalledTimes(2));
		const retry = await screen.findByRole("button", { name: "Delete" });
		await waitFor(() => expect(retry).toBeEnabled());
		await user.click(retry);

		await waitFor(() => expect(remove).toHaveBeenCalledTimes(2));
	});

	it("keeps each pending toggle on its own row", async () => {
		const user = userEvent.setup();
		const { getDefinitions } = renderPage();
		const other = {
			...MockForkAgentDefinitionMetadata,
			id: "agent-definition-release-notes",
			name: "release-notes",
		};
		getDefinitions.mockResolvedValue([MockForkAgentDefinitionMetadata, other]);
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateUserAgentDefinition")
			.mockImplementation(() => new Promise(() => {}));
		const firstSwitch = await screen.findByRole("switch", {
			name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
		});

		await user.click(firstSwitch);
		await user.click(
			await screen.findByRole("switch", { name: `Enable ${other.name}` }),
		);
		await user.click(firstSwitch);

		await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
		expect(update.mock.calls.map(([, name]) => name)).toEqual([
			MockForkAgentDefinitionMetadata.name,
			other.name,
		]);
	});

	it("keeps a pending toggle in its own organization", async () => {
		const user = userEvent.setup();
		renderPage();
		let resolveFirst: (definition: ForkAgentDefinition) => void = () => {};
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateUserAgentDefinition")
			.mockImplementationOnce(
				() =>
					new Promise((resolve) => {
						resolveFirst = resolve;
					}),
			)
			.mockImplementation(() => new Promise(() => {}));
		const switchName = `Enable ${MockForkAgentDefinitionMetadata.name}`;
		const selectOrganization = async (from: Organization, to: Organization) => {
			await user.click(
				await screen.findByRole("combobox", {
					name: `Organization ${from.display_name}`,
				}),
			);
			// Option names start with the avatar's initials.
			await user.click(
				screen.getByRole("option", { name: new RegExp(`${to.display_name}$`) }),
			);
			await screen.findByRole("combobox", {
				name: `Organization ${to.display_name}`,
			});
		};

		await user.click(await screen.findByRole("switch", { name: switchName }));
		await selectOrganization(MockDefaultOrganization, MockOrganization2);
		await user.click(await screen.findByRole("switch", { name: switchName }));
		await waitFor(() =>
			expect(update).toHaveBeenLastCalledWith(
				MockOrganization2.id,
				MockForkAgentDefinitionMetadata.name,
				{ enabled: false },
			),
		);

		// The first request settles after the switch and must release its
		// own organization's row, not the other organization's.
		resolveFirst({
			...MockForkAgentDefinitionMetadata,
			enabled: false,
			content: "",
		});
		await selectOrganization(MockOrganization2, MockDefaultOrganization);
		const firstSwitch = await screen.findByRole("switch", { name: switchName });
		await waitFor(() => expect(firstSwitch).toBeEnabled());
		await user.click(firstSwitch);

		await waitFor(() => expect(update).toHaveBeenCalledTimes(3));
		expect(update).toHaveBeenLastCalledWith(
			MockDefaultOrganization.id,
			MockForkAgentDefinitionMetadata.name,
			{ enabled: false },
		);
	});
});
