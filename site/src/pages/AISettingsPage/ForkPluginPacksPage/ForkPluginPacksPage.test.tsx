import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AxiosError } from "axios";
import { createMemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkPluginPacksApi from "#/api/forkPluginPacks";
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
vi.mock("#/modules/dashboard/useDashboard", () => ({
	useDashboard: () => ({
		experiments: ["fork-plugin-packs"],
		organizations: [MockOrganization, MockOrganization2],
	}),
}));

const path = "/ai/settings/plugin-packs";

const renderPage = () =>
	renderWithRouter(
		createMemoryRouter([{ path, element: <ForkPluginPacksPage /> }], {
			initialEntries: [`${path}?org=${MockOrganization2.name}`],
		}),
	);

beforeEach(() => {
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
	renderPage();

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
	renderPage();

	await userEvent.click(await screen.findByRole("button", { name: "Retry" }));

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2),
	);
});

it("closes the install dialog when a lost response did install the pack", async () => {
	const user = userEvent.setup();
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
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
	);
	expect(screen.getByText(MockForkPluginPack.name)).toBeInTheDocument();
});

it("reports an update as done when a lost response did update the pack", async () => {
	const user = userEvent.setup();
	const toastSuccess = vi.spyOn(toast, "success");
	const toastError = vi.spyOn(toast, "error");
	const update = vi
		.spyOn(forkPluginPacksApi, "updatePluginPack")
		.mockRejectedValue(new AxiosError("Network Error", "ERR_NETWORK"));
	const updated = {
		...MockForkPluginPack,
		commit_sha: "fedcba9876543210fedcba9876543210fedcba98",
		updated_at: "2026-11-12T12:00:00Z",
	};
	vi.mocked(forkPluginPacksApi.getPluginPacks)
		.mockResolvedValueOnce([MockForkPluginPack])
		.mockResolvedValue([updated]);
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
