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
	mockApiError,
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

	it("shares a personal workflow with the organization", async () => {
		const user = userEvent.setup();
		const { listUser, listOrganization } = renderPage();
		const shared = {
			...MockForkSavedWorkflowMetadata,
			id: "saved-workflow-shared",
			user_id: undefined,
			created_by: MockUserMember.id,
		};
		const share = vi
			.spyOn(forkSavedWorkflowsApi, "shareUserSavedWorkflow")
			.mockImplementation(async () => {
				listUser.mockResolvedValue([]);
				listOrganization.mockResolvedValue([
					MockForkOrganizationSavedWorkflowMetadata,
					shared,
				]);
				return { ...shared, script: MockForkWorkflowScript };
			});

		const personal = await screen.findByRole("table", {
			name: "Personal workflows",
		});
		await user.click(
			await within(personal).findByRole("button", {
				name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
			}),
		);
		await user.click(
			await screen.findByRole("menuitem", {
				name: "Share with organization…",
			}),
		);
		const dialog = await screen.findByRole("dialog", {
			name: `Share ${MockForkSavedWorkflowMetadata.name} with the organization?`,
		});
		await user.click(within(dialog).getByRole("button", { name: "Share" }));

		await waitFor(() =>
			expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
		);
		expect(share).toHaveBeenCalledWith(
			MockDefaultOrganization.id,
			MockForkSavedWorkflowMetadata.name,
			MockForkSavedWorkflowMetadata.id,
		);
		await within(personal).findByText(
			"No personal workflows in this organization yet.",
		);
		const organization = screen.getByRole("table", {
			name: "Organization workflows",
		});
		// The member who shared it can edit and delete it; the other
		// organization workflow stays read-only.
		await user.click(
			await within(organization).findByRole("button", {
				name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
			}),
		);
		await screen.findByRole("menuitem", { name: "Edit" });
		screen.getByRole("menuitem", { name: "Delete…" });
		expect(
			screen.queryByRole("menuitem", { name: "Share with organization…" }),
		).toBeNull();
		expect(
			within(organization).queryByRole("button", {
				name: `More actions for ${MockForkOrganizationSavedWorkflowMetadata.name}`,
			}),
		).toBeNull();
	});

	it("keeps the share dialog open with the error when the move fails", async () => {
		const user = userEvent.setup();
		renderPage();
		vi.spyOn(forkSavedWorkflowsApi, "shareUserSavedWorkflow").mockRejectedValue(
			mockApiError({
				message: "An organization workflow with this name already exists.",
				status: 409,
			}),
		);

		const personal = await screen.findByRole("table", {
			name: "Personal workflows",
		});
		await user.click(
			await within(personal).findByRole("button", {
				name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
			}),
		);
		await user.click(
			await screen.findByRole("menuitem", {
				name: "Share with organization…",
			}),
		);
		const dialog = await screen.findByRole("dialog");
		await user.click(within(dialog).getByRole("button", { name: "Share" }));

		await within(dialog).findByText(
			"An organization workflow with this name already exists.",
		);
		expect(screen.getByRole("dialog")).toBeInTheDocument();
		within(personal).getByText(MockForkSavedWorkflowMetadata.name);
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
	});

	it("asks a user without an organization to join one", async () => {
		const { listUser } = renderPage({ organizationIds: [] });

		await screen.findByText("Join an organization to save workflows.");
		expect(listUser).not.toHaveBeenCalled();
	});
});
