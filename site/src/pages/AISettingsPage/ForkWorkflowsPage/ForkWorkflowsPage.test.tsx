import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router";
import { beforeEach, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkSavedWorkflowsApi from "#/api/forkSavedWorkflows";
import { organizationSavedWorkflowACLKey } from "#/api/queries/forkSavedWorkflows";
import {
	MockNoPermissions,
	MockOrganization,
	MockOrganization2,
	MockOrganizationSkillACL,
	MockUserOwner,
	mockApiError,
} from "#/testHelpers/entities";
import {
	MockForkOrganizationSavedWorkflowMetadata,
	MockForkSavedWorkflow,
	MockForkSavedWorkflowMetadata,
} from "#/testHelpers/forkSavedWorkflows";
import { MockForkWorkflowScript } from "#/testHelpers/forkWorkflows";
import { renderWithRouter } from "#/testHelpers/renderHelpers";
import ForkWorkflowsPage from "./ForkWorkflowsPage";

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

const path = "/ai/settings/workflows";

// A short script the form accepts; userEvent.type cannot type the full
// MockForkWorkflowScript, whose brackets parse as key descriptors.
const typedScript = 'def main(args):\n    log("hi")';

const renderPage = (search: string) => {
	const router = createMemoryRouter(
		[
			{ path, element: <ForkWorkflowsPage /> },
			{ path: "/ai/settings", element: null },
		],
		{ initialEntries: [`${path}${search}`] },
	);
	return renderWithRouter(router);
};

beforeEach(() => {
	dashboard.experiments = ["fork-workflows"];
	vi.spyOn(API, "checkAuthorization").mockImplementation(async ({ checks }) =>
		Object.fromEntries(Object.keys(checks).map((key) => [key, true])),
	);
});

it("lists the personal and organization workflows of the organization the URL names", async () => {
	const listUser = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows")
		.mockResolvedValue([MockForkSavedWorkflowMetadata]);
	const listOrganization = vi
		.spyOn(forkSavedWorkflowsApi, "getOrganizationSavedWorkflows")
		.mockResolvedValue([MockForkOrganizationSavedWorkflowMetadata]);
	renderPage(`?org=${MockOrganization2.name}`);

	await screen.findByText(MockForkSavedWorkflowMetadata.name);
	await waitFor(() =>
		expect(listOrganization).toHaveBeenCalledWith(MockOrganization2.id),
	);
	expect(listUser).toHaveBeenCalledWith(MockOrganization2.id);
	expect(listUser).not.toHaveBeenCalledWith(MockOrganization.id);
});

it("switches the organization through the picker", async () => {
	const user = userEvent.setup();
	const list = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows")
		.mockResolvedValue([MockForkSavedWorkflowMetadata]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	const { router } = renderPage("");
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
	const list = vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows");
	const { router } = renderPage(`?org=${MockOrganization.name}`);

	await waitFor(() =>
		expect(router.state.location.pathname).toBe("/ai/settings"),
	);
	expect(list).not.toHaveBeenCalled();
});

it("opens the sharing dialog and returns focus to the row menu", async () => {
	const user = userEvent.setup();
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([MockForkSavedWorkflowMetadata]);
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue(
		[],
	);
	const getACL = vi
		.spyOn(forkSavedWorkflowsApi, "getOrganizationSavedWorkflowACL")
		.mockResolvedValue(MockOrganizationSkillACL);
	renderPage(`?org=${MockOrganization2.name}`);

	const menuButton = await screen.findByRole("button", {
		name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
	});
	await user.click(menuButton);
	await user.click(
		await screen.findByRole("menuitem", { name: "Manage permissions" }),
	);

	const dialog = await screen.findByRole("dialog", {
		name: "Saved workflow permissions",
	});
	expect(getACL).toHaveBeenCalledWith(
		MockOrganization2.id,
		MockForkSavedWorkflowMetadata.name,
	);
	await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
	await waitFor(() => expect(menuButton).toHaveFocus());
});

it("closes the sharing dialog when navigation changes the organization", async () => {
	const user = userEvent.setup();
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([MockForkSavedWorkflowMetadata]);
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue(
		[],
	);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflowACL",
	).mockResolvedValue(MockOrganizationSkillACL);
	const { router, queryClient } = renderPage(`?org=${MockOrganization2.name}`);
	const aclQueryKey = organizationSavedWorkflowACLKey(
		MockOrganization2.id,
		MockForkSavedWorkflowMetadata.name,
	);
	const aclObservers = () =>
		queryClient
			.getQueryCache()
			.find({ queryKey: aclQueryKey, exact: true })
			?.getObserversCount() ?? 0;

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(
		await screen.findByRole("menuitem", { name: "Manage permissions" }),
	);
	await waitFor(() => expect(aclObservers()).toBeGreaterThan(0));

	await router.navigate(`${path}?org=${MockOrganization.name}`);

	await waitFor(() => expect(aclObservers()).toBe(0));
});

it("creates a workflow from the editor form", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue(
		[],
	);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	const create = vi
		.spyOn(forkSavedWorkflowsApi, "createUserSavedWorkflow")
		.mockResolvedValue(MockForkSavedWorkflow);
	renderPage(`?org=${MockOrganization2.name}`);

	const createButtons = await screen.findAllByRole("button", {
		name: "New workflow",
	});
	await user.click(createButtons[0]);
	const dialog = await screen.findByRole("dialog");
	await user.type(
		within(dialog).getByLabelText("Name"),
		MockForkSavedWorkflowMetadata.name,
	);
	await user.type(within(dialog).getByLabelText("Script"), typedScript);
	await user.click(
		within(dialog).getByRole("button", { name: "Create workflow" }),
	);

	await waitFor(() =>
		expect(create).toHaveBeenCalledWith(
			MockOrganization2.id,
			expect.objectContaining({
				name: MockForkSavedWorkflowMetadata.name,
				script: typedScript,
			}),
		),
	);
});

it("edits a workflow and sends only the fields the form owns", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue([
		MockForkSavedWorkflowMetadata,
	]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockResolvedValue(
		MockForkSavedWorkflow,
	);
	const update = vi
		.spyOn(forkSavedWorkflowsApi, "updateUserSavedWorkflow")
		.mockResolvedValue(MockForkSavedWorkflow);
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	await user.type(
		await within(dialog).findByLabelText("Description"),
		" even harder",
	);
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);

	await waitFor(() =>
		expect(update).toHaveBeenCalledWith(
			MockOrganization2.id,
			MockForkSavedWorkflowMetadata.name,
			expect.objectContaining({
				description: `${MockForkSavedWorkflowMetadata.description} even harder`,
				script: MockForkWorkflowScript.trimEnd(),
				args_schema: MockForkSavedWorkflowMetadata.args_schema,
				phases: ["review", "summarize"],
			}),
		),
	);
});

it("shows a server validation error in the editor", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue([
		MockForkSavedWorkflowMetadata,
	]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockResolvedValue(
		MockForkSavedWorkflow,
	);
	const update = vi
		.spyOn(forkSavedWorkflowsApi, "updateUserSavedWorkflow")
		.mockRejectedValue(
			mockApiError({
				message: "Invalid saved workflow.",
				detail: "the script must define main(args)",
				status: 400,
			}),
		);
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	const script = await within(dialog).findByLabelText("Script");
	await user.clear(script);
	// A script without main(args) cannot reach the server, so keep one the
	// form accepts and let the server reject it.
	await user.type(script, "def main(args):\n    log('broken')\n");
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);

	// The script the form accepts but the server rejects proves the error
	// shown came from the response, not the form validation.
	await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
	await within(dialog).findByText("Invalid saved workflow.");
});

it("keeps the editor open when a lost response did not save the edit", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue([
		MockForkSavedWorkflowMetadata,
	]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	// The refetched workflow holds the submitted script, so a script-only
	// check would count this lost-response save as committed.
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockResolvedValue({
		...MockForkSavedWorkflow,
		script: MockForkWorkflowScript.trimEnd(),
	});
	// An axios error without a response, which the editor treats as an
	// unknown outcome that the refetched workflow must prove committed.
	const update = vi
		.spyOn(forkSavedWorkflowsApi, "updateUserSavedWorkflow")
		.mockRejectedValue({ isAxiosError: true });
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	// Only the description changes, so the refetched workflow holds the
	// submitted script and must not count as a committed save.
	await user.type(
		await within(dialog).findByLabelText("Description"),
		" even harder",
	);
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);

	await waitFor(() => expect(update).toHaveBeenCalledTimes(1));

	// The editor stays open with the draft, so the user can retry the save.
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);
	await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
});

it("deletes a workflow after confirmation", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue([
		MockForkSavedWorkflowMetadata,
	]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	const remove = vi
		.spyOn(forkSavedWorkflowsApi, "deleteUserSavedWorkflow")
		.mockResolvedValue();
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: "Delete…" }));
	await user.click(await screen.findByRole("button", { name: "Delete" }));

	await waitFor(() =>
		expect(remove).toHaveBeenCalledWith(
			MockOrganization2.id,
			MockForkSavedWorkflowMetadata.name,
		),
	);
});

it("opens the read-only detail dialog with the script", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue([
		MockForkSavedWorkflowMetadata,
	]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	const get = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow")
		.mockResolvedValue(MockForkSavedWorkflow);
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: MockForkSavedWorkflowMetadata.name,
		}),
	);

	await screen.findByLabelText("Script");
	expect(get).toHaveBeenCalledWith(
		MockOrganization2.id,
		MockForkSavedWorkflowMetadata.name,
	);
});
