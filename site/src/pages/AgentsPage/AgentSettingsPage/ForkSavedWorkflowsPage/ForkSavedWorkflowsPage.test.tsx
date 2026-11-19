import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkSavedWorkflowsApi from "#/api/forkSavedWorkflows";
import type { Experiment } from "#/api/typesGenerated";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockOrganization3,
	MockUserMember,
} from "#/testHelpers/entities";
import {
	MockForkOrganizationSavedWorkflowMetadata,
	MockForkSavedWorkflowMetadata,
} from "#/testHelpers/forkSavedWorkflows";
import { MockForkWorkflowScript } from "#/testHelpers/forkWorkflows";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import ForkSavedWorkflowsPage from "./ForkSavedWorkflowsPage";

const renderPage = ({
	experiments = ["fork-workflows"],
	organizationIds = [MockOrganization2.id, MockDefaultOrganization.id],
}: {
	experiments?: Experiment[];
	organizationIds?: string[];
} = {}) => {
	vi.spyOn(API, "getExperiments").mockResolvedValue(experiments);
	vi.spyOn(API, "getAuthenticatedUser").mockResolvedValue({
		...MockUserMember,
		organization_ids: organizationIds,
	});
	// MockOrganization3 is not among the member's organizations.
	vi.spyOn(API, "getOrganizations").mockResolvedValue([
		MockOrganization2,
		MockOrganization3,
		MockDefaultOrganization,
	]);
	const listUser = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows")
		.mockResolvedValue([MockForkSavedWorkflowMetadata]);
	const listOrganization = vi
		.spyOn(forkSavedWorkflowsApi, "getOrganizationSavedWorkflows")
		.mockResolvedValue([MockForkOrganizationSavedWorkflowMetadata]);
	const { router } = renderWithAuth(<ForkSavedWorkflowsPage />, {
		path: "/agents/settings/workflows",
		route: "/agents/settings/workflows",
		extraRoutes: [{ path: "/agents/settings", element: <div /> }],
	});
	return { listUser, listOrganization, router };
};

describe("ForkSavedWorkflowsPage", () => {
	it("redirects to the settings index without listing when the experiment is off", async () => {
		const { listUser, listOrganization, router } = renderPage({
			experiments: [],
		});

		await waitFor(() =>
			expect(router.state.location.pathname).toBe("/agents/settings"),
		);
		expect(listUser).not.toHaveBeenCalled();
		expect(listOrganization).not.toHaveBeenCalled();
	});

	it("lets a member manage personal workflows and only read organization ones", async () => {
		const { listUser, listOrganization } = renderPage();

		const personal = await screen.findByRole("table", {
			name: "Personal workflows",
		});
		const organization = await screen.findByRole("table", {
			name: "Organization workflows",
		});
		await within(personal).findByText(MockForkSavedWorkflowMetadata.name);
		await within(organization).findByText(
			MockForkOrganizationSavedWorkflowMetadata.name,
		);
		expect(listUser).toHaveBeenCalledWith(MockDefaultOrganization.id);
		expect(listOrganization).toHaveBeenCalledWith(MockDefaultOrganization.id);

		// Only the personal tier has a create button and row actions.
		expect(
			screen.getAllByRole("button", { name: "New workflow" }),
		).toHaveLength(1);
		within(personal).getByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		});
		expect(
			screen.queryByRole("button", {
				name: `More actions for ${MockForkOrganizationSavedWorkflowMetadata.name}`,
			}),
		).toBeNull();
	});

	it("shows an organization workflow read-only in its detail dialog", async () => {
		const user = userEvent.setup();
		renderPage();
		const getOrganization = vi
			.spyOn(forkSavedWorkflowsApi, "getOrganizationSavedWorkflow")
			.mockResolvedValue({
				...MockForkOrganizationSavedWorkflowMetadata,
				script: MockForkWorkflowScript,
			});

		await user.click(
			await screen.findByRole("button", {
				name: MockForkOrganizationSavedWorkflowMetadata.name,
			}),
		);

		const dialog = await screen.findByRole("dialog");
		await within(dialog).findByText(
			MockForkOrganizationSavedWorkflowMetadata.description,
		);
		expect(getOrganization).toHaveBeenCalledWith(
			MockDefaultOrganization.id,
			MockForkOrganizationSavedWorkflowMetadata.name,
		);
	});

	it("offers only the member's organizations in the picker", async () => {
		const user = userEvent.setup();
		const { listUser, listOrganization } = renderPage();
		await waitFor(() =>
			expect(listUser).toHaveBeenCalledWith(MockDefaultOrganization.id),
		);

		await user.click(
			await screen.findByRole("combobox", {
				name: `Organization ${MockDefaultOrganization.display_name}`,
			}),
		);
		// MockOrganization3 is listed but the member does not belong to it.
		expect(screen.getAllByRole("option")).toHaveLength(2);
		await user.click(
			screen.getByRole("option", {
				name: new RegExp(MockOrganization2.display_name),
			}),
		);

		await waitFor(() =>
			expect(listUser).toHaveBeenLastCalledWith(MockOrganization2.id),
		);
		expect(listOrganization).toHaveBeenLastCalledWith(MockOrganization2.id);
		expect(listUser).not.toHaveBeenCalledWith(MockOrganization3.id);
	});

	it("asks a user without an organization to join one", async () => {
		const { listUser } = renderPage({ organizationIds: [] });

		await screen.findByText("Join an organization to save workflows.");
		expect(listUser).not.toHaveBeenCalled();
	});
});
