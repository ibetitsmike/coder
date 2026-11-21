import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkSavedWorkflowsApi from "#/api/forkSavedWorkflows";
import type { Experiment } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
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
	MockForkSharedSavedWorkflowMetadata,
} from "#/testHelpers/forkSavedWorkflows";
import { MockForkWorkflowScript } from "#/testHelpers/forkWorkflows";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import ForkSavedWorkflowsPage from "./ForkSavedWorkflowsPage";

const renderPage = ({
	experiments = ["fork-workflows"],
	organizationIds = [MockOrganization2.id, MockDefaultOrganization.id],
	route = "/agents/settings/workflows",
}: {
	experiments?: Experiment[];
	organizationIds?: string[];
	route?: string;
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
		route,
		extraRoutes: [{ path: "/agents/settings", element: <div /> }],
	});
	return { listUser, listOrganization, router };
};

const openShareDialog = async (user: ReturnType<typeof userEvent.setup>) => {
	const personal = await screen.findByRole("table", {
		name: "Personal workflows",
	});
	await user.click(
		await within(personal).findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(
		await screen.findByRole("menuitem", { name: "Share with organization…" }),
	);
	return screen.findByRole("dialog", {
		name: `Share ${MockForkSavedWorkflowMetadata.name} with the organization?`,
	});
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
		const success = vi.spyOn(toast, "success");
		const { listUser, listOrganization } = renderPage();
		const share = vi
			.spyOn(forkSavedWorkflowsApi, "shareUserSavedWorkflow")
			.mockImplementation(async () => {
				listUser.mockResolvedValue([]);
				listOrganization.mockResolvedValue([
					MockForkOrganizationSavedWorkflowMetadata,
					MockForkSharedSavedWorkflowMetadata,
				]);
				return {
					...MockForkSharedSavedWorkflowMetadata,
					script: MockForkWorkflowScript,
				};
			});

		const dialog = await openShareDialog(user);
		const userCalls = listUser.mock.calls.length;
		const organizationCalls = listOrganization.mock.calls.length;
		await user.click(within(dialog).getByRole("button", { name: "Share" }));

		await waitFor(() =>
			expect(success).toHaveBeenCalledWith(
				`${MockForkSavedWorkflowMetadata.name} is now an organization workflow.`,
			),
		);
		expect(share).toHaveBeenCalledWith(
			MockDefaultOrganization.id,
			MockForkSavedWorkflowMetadata.name,
			{ expect_id: MockForkSavedWorkflowMetadata.id },
		);
		await waitFor(() =>
			expect(listUser.mock.calls.length).toBeGreaterThan(userCalls),
		);
		await waitFor(() =>
			expect(listOrganization.mock.calls.length).toBeGreaterThan(
				organizationCalls,
			),
		);
	});

	it("lets the member retry a share the server rejected", async () => {
		const user = userEvent.setup();
		const success = vi.spyOn(toast, "success");
		renderPage();
		const share = vi
			.spyOn(forkSavedWorkflowsApi, "shareUserSavedWorkflow")
			.mockRejectedValueOnce(
				mockApiError({
					message: "The organization already has a workflow with this name.",
					status: 409,
				}),
			)
			.mockResolvedValueOnce({
				...MockForkSharedSavedWorkflowMetadata,
				script: MockForkWorkflowScript,
			});

		const dialog = await openShareDialog(user);
		const confirm = within(dialog).getByRole("button", { name: "Share" });
		await user.click(confirm);
		await waitFor(() => expect(share).toHaveBeenCalledTimes(1));
		expect(success).not.toHaveBeenCalled();

		// The button ignores clicks until the rejected request settles.
		await waitFor(async () => {
			await user.click(confirm);
			expect(share).toHaveBeenCalledTimes(2);
		});
		await waitFor(() =>
			expect(success).toHaveBeenCalledWith(
				`${MockForkSavedWorkflowMetadata.name} is now an organization workflow.`,
			),
		);
		expect(share).toHaveBeenCalledTimes(2);
	});

	it("tells the member when the workflow to share is already gone", async () => {
		const user = userEvent.setup();
		const info = vi.spyOn(toast, "info");
		const success = vi.spyOn(toast, "success");
		const { listUser } = renderPage();
		const share = vi
			.spyOn(forkSavedWorkflowsApi, "shareUserSavedWorkflow")
			.mockRejectedValue(
				mockApiError({ message: "Resource not found", status: 404 }),
			);

		const dialog = await openShareDialog(user);
		const userCalls = listUser.mock.calls.length;
		// The refetch never settles, so only the mutation can drop the row.
		listUser.mockReturnValue(new Promise(() => {}));
		await user.click(within(dialog).getByRole("button", { name: "Share" }));

		await waitFor(() =>
			expect(info).toHaveBeenCalledWith(
				`${MockForkSavedWorkflowMetadata.name} is no longer available to share, so nothing was shared.`,
			),
		);
		expect(share).toHaveBeenCalledTimes(1);
		expect(success).not.toHaveBeenCalled();
		await waitFor(() =>
			expect(listUser.mock.calls.length).toBeGreaterThan(userCalls),
		);
		// The row left before the dialog closed, so focus went to the
		// table's fallback, not to the removed row's menu.
		await waitFor(() =>
			expect(
				screen.getAllByRole("button", { name: "New workflow" })[0],
			).toHaveFocus(),
		);
	});

	it("selects the organization the URL names before the default one", async () => {
		const { listUser, listOrganization } = renderPage({
			route: `/agents/settings/workflows?org=${MockOrganization2.name}`,
		});

		await screen.findByRole("table", { name: "Personal workflows" });
		expect(listUser).toHaveBeenCalledWith(MockOrganization2.id);
		expect(listOrganization).toHaveBeenCalledWith(MockOrganization2.id);
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
