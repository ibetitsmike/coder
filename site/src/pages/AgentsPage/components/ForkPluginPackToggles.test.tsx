import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as enablementApi from "#/api/forkPluginPackEnablement";
import * as forkPluginPacksApi from "#/api/forkPluginPacks";
import { toast } from "#/components/Toaster/toast";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import { render } from "#/testHelpers/renderHelpers";
import { ForkPluginPackToggles } from "./ForkPluginPackToggles";

const target = { kind: "chat", chatId: "chat-1" } as const;

beforeEach(() => {
	vi.spyOn(enablementApi, "getEnabledPluginPacks").mockResolvedValue([
		MockForkPluginPackWithoutPath,
	]);
	vi.spyOn(enablementApi, "setPluginPackEnabled").mockResolvedValue();
});

afterEach(() => {
	vi.restoreAllMocks();
});

it("enables an installed pack for the target", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
		MockForkPluginPackWithoutPath,
	]);
	render(
		<ForkPluginPackToggles
			target={target}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);

	const toggle = await screen.findByRole("switch", {
		name: `Enable ${MockForkPluginPack.name}`,
	});
	await user.click(toggle);

	await waitFor(() =>
		expect(enablementApi.setPluginPackEnabled).toHaveBeenCalledWith(
			target,
			MockForkPluginPack.id,
			true,
		),
	);
});

it("lists only enabled packs when the organization's packs cannot be listed", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockRejectedValue({
		...mockApiError({ message: "Forbidden." }),
		status: 403,
	});
	render(
		<ForkPluginPackToggles
			target={target}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);

	const toggle = await screen.findByRole("switch", {
		name: `Enable ${MockForkPluginPackWithoutPath.name}`,
	});
	await user.click(toggle);

	await waitFor(() =>
		expect(enablementApi.setPluginPackEnabled).toHaveBeenCalledWith(
			target,
			MockForkPluginPackWithoutPath.id,
			false,
		),
	);
});

it("reports a failed toggle after the switches unmount", async () => {
	const user = userEvent.setup();
	const toastError = vi.spyOn(toast, "error");
	let rejectToggle: (error: unknown) => void = () => {};
	vi.mocked(enablementApi.setPluginPackEnabled).mockReturnValueOnce(
		new Promise((_resolve, reject) => {
			rejectToggle = reject;
		}),
	);
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
	]);
	const { unmount } = render(
		<ForkPluginPackToggles
			target={target}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);

	await user.click(
		await screen.findByRole("switch", {
			name: `Enable ${MockForkPluginPack.name}`,
		}),
	);
	await waitFor(() =>
		expect(enablementApi.setPluginPackEnabled).toHaveBeenCalledTimes(1),
	);
	unmount();
	rejectToggle(mockApiError({ message: "Failed to enable the plugin pack." }));

	await waitFor(() =>
		expect(toastError).toHaveBeenCalledWith(
			"Failed to enable the plugin pack.",
			expect.objectContaining({}),
		),
	);
});
