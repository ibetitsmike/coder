import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AxiosError } from "axios";
import { createMemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkPluginPacksApi from "#/api/forkPluginPacks";
import type { ForkPluginPack } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import {
	MockNoPermissions,
	MockOrganization,
	MockOrganization2,
	MockUserOwner,
	mockApiError,
} from "#/testHelpers/entities";
import { MockForkPluginPack } from "#/testHelpers/forkPluginPacks";
import { renderWithRouter } from "#/testHelpers/renderHelpers";
import ForkPluginPacksPage from "./ForkPluginPacksPage";

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

const path = "/ai/settings/plugin-packs";

const renderPage = (search = `?org=${MockOrganization2.name}`) =>
	renderWithRouter(
		createMemoryRouter(
			[
				{ path, element: <ForkPluginPacksPage /> },
				{ path: "/ai/settings", element: null },
			],
			{ initialEntries: [`${path}${search}`] },
		),
	);

beforeEach(() => {
	dashboard.experiments = ["fork-plugin-packs"];
	vi.spyOn(API, "checkAuthorization").mockImplementation(async ({ checks }) =>
		Object.fromEntries(Object.keys(checks).map((key) => [key, true])),
	);
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
	]);
});

afterEach(() => {
	vi.restoreAllMocks();
});

it("lists the packs of the organization the URL names", async () => {
	renderPage(`?org=${MockOrganization2.name}`);

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledWith(
			MockOrganization2.id,
		),
	);
	expect(forkPluginPacksApi.getPluginPacks).not.toHaveBeenCalledWith(
		MockOrganization.id,
	);
});

it("installs a pack in the organization the URL names", async () => {
	const user = userEvent.setup();
	const create = vi
		.spyOn(forkPluginPacksApi, "createPluginPack")
		.mockResolvedValue(MockForkPluginPack);
	renderPage();

	await user.click(
		await screen.findByRole("button", { name: "Install plugin pack" }),
	);
	const dialog = await screen.findByRole("dialog");
	await user.type(within(dialog).getByLabelText(/^Name/), "pstack");
	await user.type(
		within(dialog).getByLabelText(/^Git repository URL/),
		"https://git.example.com/team/plugins.git",
	);
	await user.type(within(dialog).getByLabelText(/^Ref/), "v0.15.36");
	await user.click(within(dialog).getByRole("button", { name: "Install" }));

	await waitFor(() =>
		expect(create).toHaveBeenCalledWith(MockOrganization2.id, {
			name: "pstack",
			source_url: "https://git.example.com/team/plugins.git",
			source_path: undefined,
			ref: "v0.15.36",
		}),
	);
});

it("updates and deletes a pack from its row menu", async () => {
	const user = userEvent.setup();
	const update = vi
		.spyOn(forkPluginPacksApi, "updatePluginPack")
		.mockResolvedValue(MockForkPluginPack);
	const remove = vi
		.spyOn(forkPluginPacksApi, "deletePluginPack")
		.mockResolvedValue();
	renderPage();
	const menu = await screen.findByRole("button", {
		name: `More actions for ${MockForkPluginPack.name}`,
	});

	await user.click(menu);
	await user.click(
		await screen.findByRole("menuitem", {
			name: /Update to the latest commit/,
		}),
	);
	await waitFor(() =>
		expect(update).toHaveBeenCalledWith(
			MockOrganization2.id,
			MockForkPluginPack.id,
			{},
		),
	);

	await user.click(menu);
	await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));
	await user.click(
		within(await screen.findByRole("dialog")).getByRole("button", {
			name: "Delete",
		}),
	);
	await waitFor(() =>
		expect(remove).toHaveBeenCalledWith(
			MockOrganization2.id,
			MockForkPluginPack.id,
		),
	);
});

it("requests the packs again when Retry is clicked after a failed load", async () => {
	vi.mocked(forkPluginPacksApi.getPluginPacks)
		.mockRejectedValueOnce(
			mockApiError({ message: "Failed to list plugin packs." }),
		)
		.mockResolvedValueOnce([MockForkPluginPack]);
	renderPage(`?org=${MockOrganization2.name}`);

	await userEvent.click(await screen.findByRole("button", { name: "Retry" }));

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2),
	);
});

it("reports an install as done when a lost response did install the pack", async () => {
	const user = userEvent.setup();
	const toastSuccess = vi.spyOn(toast, "success");
	vi.spyOn(forkPluginPacksApi, "createPluginPack").mockRejectedValue(
		new AxiosError("Network Error", "ERR_NETWORK"),
	);
	vi.mocked(forkPluginPacksApi.getPluginPacks).mockResolvedValueOnce([]);
	renderPage();

	await user.click(
		await screen.findByRole("button", { name: "Install plugin pack" }),
	);
	const dialog = await screen.findByRole("dialog");
	await user.type(
		within(dialog).getByLabelText(/^Name/),
		MockForkPluginPack.name,
	);
	await user.type(
		within(dialog).getByLabelText(/^Git repository URL/),
		MockForkPluginPack.source_url,
	);
	await user.type(
		within(dialog).getByLabelText(/^Ref/),
		MockForkPluginPack.ref,
	);
	await user.click(within(dialog).getByRole("button", { name: "Install" }));

	await waitFor(() =>
		expect(toastSuccess).toHaveBeenCalledWith(
			`${MockForkPluginPack.name} installed.`,
		),
	);
});

it("reports an update as done when a lost response did update the pack", async () => {
	const user = userEvent.setup();
	const toastSuccess = vi.spyOn(toast, "success");
	const toastError = vi.spyOn(toast, "error");
	const update = vi
		.spyOn(forkPluginPacksApi, "updatePluginPack")
		.mockRejectedValue(new AxiosError("Network Error", "ERR_NETWORK"));
	const mockUpdatedPack = {
		...MockForkPluginPack,
		commit_sha: "fedcba9876543210fedcba9876543210fedcba98",
		updated_at: "2026-11-12T12:00:00Z",
	};
	vi.mocked(forkPluginPacksApi.getPluginPacks)
		.mockResolvedValueOnce([MockForkPluginPack])
		.mockResolvedValue([mockUpdatedPack]);
	renderPage();

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkPluginPack.name}`,
		}),
	);
	await user.click(
		await screen.findByRole("menuitem", {
			name: /Update to the latest commit/,
		}),
	);

	await waitFor(() =>
		expect(toastSuccess).toHaveBeenCalledWith(
			`${MockForkPluginPack.name} is at fedcba9876.`,
		),
	);
	expect(toastError).not.toHaveBeenCalled();
	expect(update).toHaveBeenCalledTimes(1);
});

it("does not report an install when a lost response leaves only an older same-name pack", async () => {
	const user = userEvent.setup();
	const toastSuccess = vi.spyOn(toast, "success");
	const create = vi
		.spyOn(forkPluginPacksApi, "createPluginPack")
		.mockRejectedValue(new AxiosError("Network Error", "ERR_NETWORK"));
	renderPage();

	await user.click(
		await screen.findByRole("button", { name: "Install plugin pack" }),
	);
	const dialog = await screen.findByRole("dialog");
	await user.type(
		within(dialog).getByLabelText(/^Name/),
		MockForkPluginPack.name,
	);
	await user.type(
		within(dialog).getByLabelText(/^Git repository URL/),
		MockForkPluginPack.source_url,
	);
	await user.type(
		within(dialog).getByLabelText(/^Ref/),
		MockForkPluginPack.ref,
	);
	await user.click(within(dialog).getByRole("button", { name: "Install" }));

	// The mutation has settled, refetch included, once Install is enabled again.
	await waitFor(() =>
		expect(
			within(dialog).getByRole("button", { name: "Install" }),
		).toBeEnabled(),
	);
	expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2);
	expect(create).toHaveBeenCalledTimes(1);
	expect(toastSuccess).not.toHaveBeenCalled();
});

it("does not report an install when a lost response follows a failed first load", async () => {
	const user = userEvent.setup();
	const toastSuccess = vi.spyOn(toast, "success");
	const create = vi
		.spyOn(forkPluginPacksApi, "createPluginPack")
		.mockRejectedValue(new AxiosError("Network Error", "ERR_NETWORK"));
	vi.mocked(forkPluginPacksApi.getPluginPacks).mockRejectedValueOnce(
		mockApiError({ message: "Failed to list plugin packs." }),
	);
	renderPage();
	await screen.findByRole("button", { name: "Retry" });

	await user.click(
		await screen.findByRole("button", { name: "Install plugin pack" }),
	);
	const dialog = await screen.findByRole("dialog");
	await user.type(
		within(dialog).getByLabelText(/^Name/),
		MockForkPluginPack.name,
	);
	await user.type(
		within(dialog).getByLabelText(/^Git repository URL/),
		MockForkPluginPack.source_url,
	);
	await user.type(
		within(dialog).getByLabelText(/^Ref/),
		MockForkPluginPack.ref,
	);
	await user.click(within(dialog).getByRole("button", { name: "Install" }));

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2),
	);
	await waitFor(() =>
		expect(
			within(dialog).getByRole("button", { name: "Install" }),
		).toBeEnabled(),
	);
	expect(create).toHaveBeenCalledTimes(1);
	expect(toastSuccess).not.toHaveBeenCalled();
});

it("moves focus to the table when the last pack is deleted without permission to install", async () => {
	const user = userEvent.setup();
	vi.spyOn(API, "checkAuthorization").mockImplementation(async ({ checks }) =>
		Object.fromEntries(
			Object.keys(checks).map((key) => [
				key,
				!key.endsWith(".createOrganizationSkill"),
			]),
		),
	);
	vi.spyOn(forkPluginPacksApi, "deletePluginPack").mockResolvedValue();
	vi.mocked(forkPluginPacksApi.getPluginPacks)
		.mockResolvedValueOnce([MockForkPluginPack])
		.mockResolvedValue([]);
	renderPage();

	await user.click(
		await screen.findByRole("button", {
			name: `More actions for ${MockForkPluginPack.name}`,
		}),
	);
	await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));
	await user.click(
		within(await screen.findByRole("dialog")).getByRole("button", {
			name: "Delete",
		}),
	);

	await waitFor(() =>
		expect(document.activeElement).toBe(
			screen.getByRole("table", { name: "Plugin packs" }),
		),
	);
});

it("keeps the row menu reachable and starts no second update while one is pending", async () => {
	const user = userEvent.setup();
	const toastSuccess = vi.spyOn(toast, "success");
	let finishUpdate: (pack: ForkPluginPack) => void = () => {};
	const update = vi
		.spyOn(forkPluginPacksApi, "updatePluginPack")
		.mockReturnValue(
			new Promise((resolve) => {
				finishUpdate = resolve;
			}),
		);
	renderPage();
	const menu = await screen.findByRole("button", {
		name: `More actions for ${MockForkPluginPack.name}`,
	});

	await user.click(menu);
	await user.click(
		await screen.findByRole("menuitem", {
			name: /Update to the latest commit/,
		}),
	);
	await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
	expect(menu).toBeEnabled();

	await user.click(menu);
	await user.click(await screen.findByRole("menuitem", { name: "Updating…" }));
	expect(update).toHaveBeenCalledTimes(1);

	finishUpdate(MockForkPluginPack);
	await waitFor(() =>
		expect(toastSuccess).toHaveBeenCalledWith(
			`${MockForkPluginPack.name} is at 0123456789.`,
		),
	);
});

it("switches the organization through the picker", async () => {
	const user = userEvent.setup();
	const { router } = renderPage("");
	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledWith(
			MockOrganization.id,
		),
	);

	await user.click(await screen.findByRole("combobox"));
	await user.click(
		await screen.findByRole("option", { name: /My Organization 2/ }),
	);

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledWith(
			MockOrganization2.id,
		),
	);
	expect(new URLSearchParams(router.state.location.search).get("org")).toBe(
		MockOrganization2.name,
	);
});

it("redirects to AI settings while the experiment is off", async () => {
	dashboard.experiments = [];
	const { router } = renderPage(`?org=${MockOrganization.name}`);

	await waitFor(() =>
		expect(router.state.location.pathname).toBe("/ai/settings"),
	);
	expect(API.checkAuthorization).not.toHaveBeenCalled();
	expect(forkPluginPacksApi.getPluginPacks).not.toHaveBeenCalled();
});
