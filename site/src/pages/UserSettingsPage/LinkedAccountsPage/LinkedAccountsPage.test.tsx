import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import {
	MockLinkablePlugin,
	MockPluginLinkCode,
	MockPluginUserLink,
} from "#/testHelpers/plugins";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import LinkedAccountsPage from "./LinkedAccountsPage";

describe("LinkedAccountsPage", () => {
	it("requests a link code for the chosen plugin", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "getLinkablePlugins").mockResolvedValue([
			MockLinkablePlugin,
		]);
		const createPluginLinkCode = vi
			.spyOn(API.experimental, "createPluginLinkCode")
			.mockResolvedValue(MockPluginLinkCode);

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
	});

	it("keeps polling for the new link after a failed fetch", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		vi.setSystemTime(new Date("2024-01-01T00:00:00Z"));
		try {
			const user = userEvent.setup({
				advanceTimers: vi.advanceTimersByTime,
			});
			const getLinkablePlugins = vi
				.spyOn(API.experimental, "getLinkablePlugins")
				.mockResolvedValue([MockLinkablePlugin]);
			vi.spyOn(API.experimental, "createPluginLinkCode").mockResolvedValue(
				MockPluginLinkCode,
			);

			renderWithAuth(<LinkedAccountsPage />);

			const section = await screen.findByRole("region", {
				name: MockLinkablePlugin.name,
			});
			await user.click(
				within(section).getByRole("button", { name: "Get link code" }),
			);
			await screen.findByText(MockPluginLinkCode.code);

			getLinkablePlugins.mockRejectedValueOnce(new Error("network blip"));
			const callsBeforeError = getLinkablePlugins.mock.calls.length;
			await vi.advanceTimersByTimeAsync(3_000);
			expect(getLinkablePlugins).toHaveBeenCalledTimes(callsBeforeError + 1);

			await vi.advanceTimersByTimeAsync(3_000);
			expect(getLinkablePlugins).toHaveBeenCalledTimes(callsBeforeError + 2);
		} finally {
			vi.useRealTimers();
		}
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
