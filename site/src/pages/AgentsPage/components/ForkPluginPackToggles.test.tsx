import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as enablementApi from "#/api/forkPluginPackEnablement";
import { chatEntityKey } from "#/api/queries/chats";
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
	vi.spyOn(enablementApi, "getAvailablePluginPacks").mockResolvedValue([
		MockForkPluginPack,
		MockForkPluginPackWithoutPath,
	]);
});

afterEach(() => {
	vi.restoreAllMocks();
});

it("enables an installed pack for the target and refetches the chat", async () => {
	const user = userEvent.setup();
	const { queryClient } = render(
		<ForkPluginPackToggles
			target={target}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);

	queryClient.setQueryData(chatEntityKey(target.chatId), {
		id: target.chatId,
	});
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
	await waitFor(() =>
		expect(
			queryClient.getQueryState(chatEntityKey(target.chatId))?.isInvalidated,
		).toBe(true),
	);
});

it("enables an installed pack for a project", async () => {
	const user = userEvent.setup();
	const projectTarget = {
		kind: "project",
		organizationId: MockForkPluginPack.organization_id,
		projectId: "project-1",
	} as const;
	render(
		<ForkPluginPackToggles
			target={projectTarget}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);

	await user.click(
		await screen.findByRole("switch", {
			name: `Enable ${MockForkPluginPack.name}`,
		}),
	);

	expect(enablementApi.getEnabledPluginPacks).toHaveBeenCalledWith(
		projectTarget,
	);
	await waitFor(() =>
		expect(enablementApi.setPluginPackEnabled).toHaveBeenCalledWith(
			projectTarget,
			MockForkPluginPack.id,
			true,
		),
	);
});

it("refetches both lists when Retry is pressed after a failed load", async () => {
	const user = userEvent.setup();
	vi.mocked(enablementApi.getEnabledPluginPacks).mockRejectedValueOnce(
		mockApiError({ message: "Failed to list enabled plugin packs." }),
	);
	render(
		<ForkPluginPackToggles
			target={target}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);

	await user.click(await screen.findByRole("button", { name: "Retry" }));

	await waitFor(() => {
		expect(enablementApi.getEnabledPluginPacks).toHaveBeenCalledTimes(2);
		expect(enablementApi.getAvailablePluginPacks).toHaveBeenCalledTimes(2);
	});
});

it("disables a pack enabled for the target", async () => {
	const user = userEvent.setup();
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
