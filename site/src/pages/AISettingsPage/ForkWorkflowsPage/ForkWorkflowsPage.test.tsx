import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router";
import { beforeEach, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkSavedWorkflowsApi from "#/api/forkSavedWorkflows";
import {
	organizationSavedWorkflowACLKey,
	savedWorkflowKey,
	savedWorkflowsKey,
} from "#/api/queries/forkSavedWorkflows";
import type { ForkSavedWorkflowMetadata } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
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

const auth = vi.hoisted((): { organizationIds: string[] } => ({
	organizationIds: [],
}));
vi.mock("#/hooks/useAuthenticated", () => ({
	useAuthenticated: () => ({
		user: { ...MockUserOwner, organization_ids: auth.organizationIds },
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
	auth.organizationIds = [MockOrganization.id, MockOrganization2.id];
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
	expect(within(dialog).getByLabelText("Name")).toHaveAccessibleDescription(
		/cannot be changed after creation/,
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
			{ expect_id: MockForkSavedWorkflow.id },
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

it("keeps the draft and refetches when the server rejects the save with 409", async () => {
	const user = userEvent.setup();
	const info = vi.spyOn(toast, "info");
	const list = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows")
		.mockResolvedValue([MockForkSavedWorkflowMetadata]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	const detail = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow")
		.mockResolvedValue(MockForkSavedWorkflow);
	// Another tab replaced the workflow after the editor loaded it, so the
	// conditional save refuses with 409, and from that moment the list and
	// detail read the replacement it stored under the same name, so the
	// refetches the mutation triggers model the server state.
	const replacement = {
		...MockForkSavedWorkflowMetadata,
		id: "saved-workflow-replacement",
		description: "Replaced in another tab.",
		updated_at: "2030-01-01T00:00:00Z",
	};
	let serverHasReplacement = false;
	const update = vi
		.spyOn(forkSavedWorkflowsApi, "updateUserSavedWorkflow")
		.mockImplementation(async () => {
			serverHasReplacement = true;
			throw mockApiError({ message: "Conflict.", status: 409 });
		});
	list.mockImplementation(async () =>
		serverHasReplacement ? [replacement] : [MockForkSavedWorkflowMetadata],
	);
	detail.mockImplementation(async () =>
		serverHasReplacement
			? { ...MockForkSavedWorkflow, ...replacement }
			: MockForkSavedWorkflow,
	);
	const { queryClient } = renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	const description = await within(dialog).findByLabelText("Description");
	// The form only seeds its values once the editor's own fetches have
	// settled, so every list call after this point is the conflict refetch.
	const listCallsBeforeSave = list.mock.calls.length;
	await user.type(description, " even harder");
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);

	await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
	expect(update).toHaveBeenCalledWith(
		MockOrganization2.id,
		MockForkSavedWorkflowMetadata.name,
		expect.anything(),
		{ expect_id: MockForkSavedWorkflow.id },
	);
	// The conflict invalidates the list and detail queries, and the
	// refetches bring the editor the state the name holds now: the cache
	// names the replacement, and the toast explains the rejection.
	await waitFor(() =>
		expect(list.mock.calls.length).toBeGreaterThan(listCallsBeforeSave),
	);
	await waitFor(() => expect(info).toHaveBeenCalled());
	await waitFor(() =>
		expect(
			queryClient
				.getQueryData<ForkSavedWorkflowMetadata[]>(
					savedWorkflowsKey({
						tier: "user",
						organizationId: MockOrganization2.id,
					}),
				)
				?.some((workflow) => workflow.id === "saved-workflow-replacement"),
		).toBe(true),
	);
	// The stale row blocks further saves, so a retry of the draft cannot
	// overwrite the replacement and the user copies it out instead.
	await user.clear(description);
	await user.type(description, "retained");
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);
	// The click reaches the editor, but the stale guard stops it before the
	// mutation: no second request, and the banner explains why.
	expect(update).toHaveBeenCalledTimes(1);
});

it("keeps the editor open with the draft after a failed save", async () => {
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
	// An axios error without a response: the outcome is unknown, so the
	// editor reports a failure and the user retries.
	const update = vi
		.spyOn(forkSavedWorkflowsApi, "updateUserSavedWorkflow")
		.mockRejectedValueOnce({ isAxiosError: true })
		.mockResolvedValue(MockForkSavedWorkflow);
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	const description = await within(dialog).findByLabelText("Description");
	await user.type(description, " even harder");
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);
	await waitFor(() => expect(update).toHaveBeenCalledTimes(1));

	expect(description).toHaveValue(
		`${MockForkSavedWorkflowMetadata.description} even harder`,
	);
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);
	await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
	await waitFor(() => expect(dialog).not.toBeInTheDocument());
});

it("keeps the draft and refetches when the server rejects the save with 404", async () => {
	const user = userEvent.setup();
	const info = vi.spyOn(toast, "info");
	const list = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows")
		.mockResolvedValue([MockForkSavedWorkflowMetadata]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockResolvedValue(
		MockForkSavedWorkflow,
	);
	// Another tab deleted the workflow after the editor loaded it, so the
	// conditional save refuses with 404, and from that moment the list no
	// longer holds the row, so the refetches the mutation triggers model
	// the server state.
	let serverDeleted = false;
	const update = vi
		.spyOn(forkSavedWorkflowsApi, "updateUserSavedWorkflow")
		.mockImplementation(async () => {
			serverDeleted = true;
			throw mockApiError({ message: "Not found.", status: 404 });
		});
	list.mockImplementation(async () =>
		serverDeleted ? [] : [MockForkSavedWorkflowMetadata],
	);
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	const description = await within(dialog).findByLabelText("Description");
	// The form only seeds its values once the editor's own fetches have
	// settled, so every list call after this point is the 404 refetch.
	const listCallsBeforeSave = list.mock.calls.length;
	await user.type(description, " even harder");
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);

	await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
	// The 404 invalidates the list and detail queries, and the refetches
	// bring the editor the state the name holds now: the row is gone, and
	// the toast explains the rejection.
	await waitFor(() =>
		expect(list.mock.calls.length).toBeGreaterThan(listCallsBeforeSave),
	);
	await waitFor(() => expect(info).toHaveBeenCalled());
	expect(description).toHaveValue(
		`${MockForkSavedWorkflowMetadata.description} even harder`,
	);
	// The stale row blocks further saves, so the draft cannot overwrite
	// anything and the user copies it out instead.
	await user.click(
		within(dialog).getByRole("button", { name: "Save workflow" }),
	);
	expect(update).toHaveBeenCalledTimes(1);
});

it("seeds the editor from a fresh fetch, not the cached workflow", async () => {
	const user = userEvent.setup();
	const newer = {
		...MockForkSavedWorkflow,
		description: "Changed in another tab.",
		updated_at: "2030-01-01T00:00:00Z",
	};
	const { script: _script, ...newerMetadata } = newer;
	// Another tab saves the change after this page cached the old version.
	let serverHasNewer = false;
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockImplementation(
		async () => [
			serverHasNewer ? newerMetadata : MockForkSavedWorkflowMetadata,
		],
	);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockImplementation(
		async () => (serverHasNewer ? newer : MockForkSavedWorkflow),
	);
	const { queryClient } = renderPage(`?org=${MockOrganization2.name}`);
	// Keep the viewed workflow cached after its dialog closes, as the app's
	// query client does.
	queryClient.setQueryDefaults(
		savedWorkflowKey(
			{ tier: "user", organizationId: MockOrganization2.id },
			MockForkSavedWorkflowMetadata.name,
		),
		{ gcTime: Number.POSITIVE_INFINITY },
	);

	// Viewing the workflow caches the version the list showed.
	await user.click(
		await screen.findByRole("button", {
			name: MockForkSavedWorkflowMetadata.name,
		}),
	);
	await screen.findByLabelText("Script");
	await user.keyboard("{Escape}");
	await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	const edit = await screen.findByRole("menuitem", { name: "Edit" });
	serverHasNewer = true;
	await user.click(edit);
	const dialog = await screen.findByRole("dialog");

	expect(await within(dialog).findByLabelText("Description")).toHaveValue(
		newer.description,
	);
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
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockResolvedValue(
		MockForkSavedWorkflow,
	);
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
			{ expect_id: MockForkSavedWorkflowMetadata.id },
		),
	);
});

it("deletes nothing when the name now holds another workflow", async () => {
	const user = userEvent.setup();
	const info = vi.spyOn(toast, "info");
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue([
		MockForkSavedWorkflowMetadata,
	]);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	// Another tab deleted the workflow and created one with the same name,
	// so the conditional delete refuses with 409.
	const remove = vi
		.spyOn(forkSavedWorkflowsApi, "deleteUserSavedWorkflow")
		.mockRejectedValue(
			mockApiError({ message: "Conflict.", status: 409 }),
		);
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: "Delete…" }));
	await user.click(await screen.findByRole("button", { name: "Delete" }));

	await waitFor(() => expect(info).toHaveBeenCalled());
	expect(remove).toHaveBeenCalledWith(
		MockOrganization2.id,
		MockForkSavedWorkflowMetadata.name,
		{ expect_id: MockForkSavedWorkflowMetadata.id },
	);
	await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("creates nothing from New workflow at the limit", async () => {
	const user = userEvent.setup();
	const workflows: ForkSavedWorkflowMetadata[] = Array.from(
		{ length: 100 },
		(_, i) => ({
			...MockForkSavedWorkflowMetadata,
			id: `saved-workflow-${i}`,
			name: `workflow-${i}`,
		}),
	);
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue(
		workflows,
	);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	const create = vi.spyOn(forkSavedWorkflowsApi, "createUserSavedWorkflow");
	renderPage(`?org=${MockOrganization2.name}`);

	await screen.findByRole("button", { name: "workflow-99" });
	// The personal tier comes first, and it holds the 100 workflows.
	const [personalNewWorkflow] = screen.getAllByRole("button", {
		name: "New workflow",
	});
	await user.click(personalNewWorkflow);
	expect(screen.queryByRole("dialog")).toBeNull();
	expect(create).not.toHaveBeenCalled();
});

it("hides the personal tier in an organization the user is not a member of", async () => {
	auth.organizationIds = [MockOrganization.id];
	const listUser = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows")
		.mockResolvedValue([]);
	const listOrganization = vi
		.spyOn(forkSavedWorkflowsApi, "getOrganizationSavedWorkflows")
		.mockResolvedValue([MockForkOrganizationSavedWorkflowMetadata]);
	renderPage(`?org=${MockOrganization2.name}`);

	await screen.findByText(MockForkOrganizationSavedWorkflowMetadata.name);
	expect(listOrganization).toHaveBeenCalledWith(MockOrganization2.id);
	expect(listUser).not.toHaveBeenCalled();
	expect(screen.queryByText("Personal workflows")).toBeNull();
	// The picker sits above both tiers, so the user can switch.
	expect(screen.getByRole("combobox")).toBeInTheDocument();
});

it("keeps the picker and its focus when a switch hides the personal tier", async () => {
	const user = userEvent.setup();
	auth.organizationIds = [MockOrganization.id];
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue(
		[],
	);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	renderPage(`?org=${MockOrganization.name}`);
	await screen.findByText("Personal workflows");

	const picker = screen.getByRole("combobox");
	await user.click(picker);
	await user.click(
		await screen.findByRole("option", { name: /My Organization 2/ }),
	);

	await waitFor(() =>
		expect(screen.queryByText("Personal workflows")).toBeNull(),
	);
	expect(screen.getByRole("combobox")).toBe(picker);
	await waitFor(() => expect(picker).toHaveFocus());
});

it("returns focus to a name button in a read-only table when the row is gone", async () => {
	const user = userEvent.setup();
	vi.spyOn(API, "checkAuthorization").mockImplementation(async ({ checks }) =>
		Object.fromEntries(
			Object.keys(checks).map((key) => [
				key,
				key.endsWith(".viewOrganizationSkills"),
			]),
		),
	);
	const second = {
		...MockForkOrganizationSavedWorkflowMetadata,
		id: "saved-workflow-org-2",
		name: "summarize-logs",
	};
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockResolvedValue(
		[],
	);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([MockForkOrganizationSavedWorkflowMetadata, second]);
	// The first workflow was deleted elsewhere, so its detail 404s and the
	// page drops its row.
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflow",
	).mockRejectedValue(mockApiError({ message: "Not found.", status: 404 }));
	renderPage(`?org=${MockOrganization2.name}`);

	await user.click(
		await screen.findByRole("button", {
			name: MockForkOrganizationSavedWorkflowMetadata.name,
		}),
	);
	await waitFor(() =>
		expect(
			screen.queryByRole("button", {
				name: MockForkOrganizationSavedWorkflowMetadata.name,
			}),
		).toBeNull(),
	);
	await user.keyboard("{Escape}");

	await waitFor(() =>
		expect(screen.getByRole("button", { name: second.name })).toHaveFocus(),
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

it("does not show a cached detail of an earlier workflow with the same name", async () => {
	const user = userEvent.setup();
	const earlier = {
		...MockForkSavedWorkflow,
		description: "Deleted in another tab.",
	};
	const { script: _script, ...earlierMetadata } = earlier;
	const recreatedMetadata = {
		...MockForkSavedWorkflowMetadata,
		id: "saved-workflow-recreated",
	};
	// Another tab deletes the workflow and creates one of the same name.
	let recreated = false;
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows").mockImplementation(
		async () => [recreated ? recreatedMetadata : earlierMetadata],
	);
	vi.spyOn(
		forkSavedWorkflowsApi,
		"getOrganizationSavedWorkflows",
	).mockResolvedValue([]);
	vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockImplementation(
		async () => {
			if (recreated) {
				throw mockApiError({ message: "Server unavailable.", status: 500 });
			}
			return earlier;
		},
	);
	const scope = { tier: "user", organizationId: MockOrganization2.id } as const;
	const { queryClient } = renderPage(`?org=${MockOrganization2.name}`);
	queryClient.setQueryDefaults(
		savedWorkflowKey(scope, MockForkSavedWorkflowMetadata.name),
		{ gcTime: Number.POSITIVE_INFINITY, retry: false },
	);

	const earlierButton = await screen.findByRole("button", {
		name: MockForkSavedWorkflowMetadata.name,
	});
	await user.click(earlierButton);
	await within(await screen.findByRole("dialog")).findByText(
		"Deleted in another tab.",
	);
	await user.keyboard("{Escape}");
	await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

	recreated = true;
	await queryClient.refetchQueries({
		queryKey: savedWorkflowsKey(scope),
		exact: true,
	});
	// The recreated workflow has a new id, so its row replaces the old one.
	await waitFor(() => expect(earlierButton).not.toBeInTheDocument());
	await user.click(
		screen.getByRole("button", {
			name: MockForkSavedWorkflowMetadata.name,
		}),
	);

	const dialog = await screen.findByRole("dialog");
	await within(dialog).findByText("Server unavailable.");
	expect(within(dialog).queryByText("Deleted in another tab.")).toBeNull();
});
