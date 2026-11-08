import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import type { LinkablePlugin } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import {
	MockLinkablePlugin,
	MockPluginConnectPrompt,
	MockPluginLinkCode,
	MockPluginUserLink,
} from "#/testHelpers/plugins";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import LinkedAccountsPage from "./LinkedAccountsPage";

const mockConnectPlugin: LinkablePlugin = {
	...MockLinkablePlugin,
	connect: true,
};

describe("LinkedAccountsPage", () => {
	it("requests a link code for the chosen plugin", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "getLinkablePlugins").mockResolvedValue([
			MockLinkablePlugin,
		]);
		const createPluginLinkCode = vi
			.spyOn(API.experimental, "createPluginLinkCode")
			.mockResolvedValue(MockPluginLinkCode);
		const connectPlugin = vi.spyOn(API.experimental, "connectPlugin");

		renderWithAuth(<LinkedAccountsPage />);

		const section = await screen.findByRole("region", {
			name: MockLinkablePlugin.name,
		});
		await user.click(
			within(section).getByRole("button", { name: "Get link code" }),
		);

		await waitFor(() =>
			expect(createPluginLinkCode).toHaveBeenCalledWith(MockLinkablePlugin.id),
		);
		expect(connectPlugin).not.toHaveBeenCalled();
	});

	it("gets the connect prompt and a link code for a plugin that connects accounts", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "getLinkablePlugins").mockResolvedValue([
			mockConnectPlugin,
		]);
		const connectPlugin = vi
			.spyOn(API.experimental, "connectPlugin")
			.mockResolvedValue(MockPluginConnectPrompt);
		const createPluginLinkCode = vi
			.spyOn(API.experimental, "createPluginLinkCode")
			.mockResolvedValue(MockPluginLinkCode);

		renderWithAuth(<LinkedAccountsPage />);

		await user.click(
			await screen.findByRole("button", { name: "Connect account" }),
		);

		await waitFor(() =>
			expect(createPluginLinkCode).toHaveBeenCalledWith(mockConnectPlugin.id),
		);
		expect(connectPlugin).toHaveBeenCalledWith(mockConnectPlugin.id);
	});

	it("creates no link code when the connect prompt fails", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "getLinkablePlugins").mockResolvedValue([
			mockConnectPlugin,
		]);
		const connectPlugin = vi
			.spyOn(API.experimental, "connectPlugin")
			.mockRejectedValue(new Error("The plugin is not running."));
		const createPluginLinkCode = vi.spyOn(
			API.experimental,
			"createPluginLinkCode",
		);
		const toastError = vi.spyOn(toast, "error");

		renderWithAuth(<LinkedAccountsPage />);

		await user.click(
			await screen.findByRole("button", { name: "Connect account" }),
		);

		await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1));
		expect(connectPlugin).toHaveBeenCalledWith(mockConnectPlugin.id);
		expect(createPluginLinkCode).not.toHaveBeenCalled();
	});

	it("unlinks an account after confirmation", async () => {
		const user = userEvent.setup();
		const getLinkablePlugins = vi
			.spyOn(API.experimental, "getLinkablePlugins")
			.mockResolvedValue([MockLinkablePlugin]);
		const deletePluginLink = vi
			.spyOn(API.experimental, "deletePluginLink")
			.mockResolvedValue();

		renderWithAuth(<LinkedAccountsPage />);

		await user.click(
			await screen.findByRole("button", {
				name: `Unlink ${MockPluginUserLink.external_id}`,
			}),
		);
		expect(deletePluginLink).not.toHaveBeenCalled();

		await user.click(
			within(screen.getByRole("dialog")).getByRole("button", {
				name: "Unlink",
			}),
		);

		await waitFor(() =>
			expect(deletePluginLink).toHaveBeenCalledWith(
				MockLinkablePlugin.id,
				MockPluginUserLink.external_id,
			),
		);
		await waitFor(() => expect(getLinkablePlugins).toHaveBeenCalledTimes(2));
	});

	it("returns focus to the Unlink button when the confirmation is canceled", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "getLinkablePlugins").mockResolvedValue([
			MockLinkablePlugin,
		]);
		const deletePluginLink = vi.spyOn(API.experimental, "deletePluginLink");

		renderWithAuth(<LinkedAccountsPage />);

		const unlinkButton = await screen.findByRole("button", {
			name: `Unlink ${MockPluginUserLink.external_id}`,
		});
		await user.click(unlinkButton);
		await user.click(
			within(screen.getByRole("dialog")).getByRole("button", {
				name: "Cancel",
			}),
		);

		await waitFor(() => expect(unlinkButton).toHaveFocus());
		expect(deletePluginLink).not.toHaveBeenCalled();
	});
});
