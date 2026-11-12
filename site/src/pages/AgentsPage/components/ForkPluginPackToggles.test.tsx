import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as enablementApi from "#/api/forkPluginPackEnablement";
import * as forkPluginPacksApi from "#/api/forkPluginPacks";
import { chatEntityKey } from "#/api/queries/chats";
import { pluginPacksKey } from "#/api/queries/forkPluginPacks";
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

it("enables an installed pack for the target and refetches the chat", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
		MockForkPluginPackWithoutPath,
	]);
	const { queryClient } = render(
		<ForkPluginPackToggles
			target={target}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);

	const invalidateQueries = vi.spyOn(queryClient, "invalidateQueries");
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
		expect(invalidateQueries).toHaveBeenCalledWith({
			queryKey: chatEntityKey(target.chatId),
			exact: true,
		}),
	);
});

it("enables an installed pack for a project", async () => {
	const user = userEvent.setup();
	const projectTarget = {
		kind: "project",
		organizationId: MockForkPluginPack.organization_id,
		projectId: "project-1",
	} as const;
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
	]);
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
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
	]);
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
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2);
	});
});

it("keeps the switches and offers Retry when a refetch fails", async () => {
	const user = userEvent.setup();
	vi.mocked(enablementApi.getEnabledPluginPacks)
		.mockResolvedValueOnce([MockForkPluginPackWithoutPath])
		.mockRejectedValueOnce(
			mockApiError({ message: "Failed to list enabled plugin packs." }),
		);
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

	await user.click(
		await screen.findByRole("switch", {
			name: `Enable ${MockForkPluginPack.name}`,
		}),
	);
	const retry = await screen.findByRole("button", { name: "Retry" });
	expect(
		screen.getByRole("switch", { name: `Enable ${MockForkPluginPack.name}` }),
	).toBeChecked();
	await user.click(retry);

	await waitFor(() =>
		expect(enablementApi.getEnabledPluginPacks).toHaveBeenCalledTimes(3),
	);
});

it("lists only enabled packs when the organization's packs cannot be listed", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockRejectedValue({
		...mockApiError({ message: "Resource not found." }),
		status: 404,
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

it("keeps enabled packs toggleable after the installed list becomes forbidden", async () => {
	const user = userEvent.setup();
	vi.spyOn(forkPluginPacksApi, "getPluginPacks")
		.mockResolvedValueOnce([MockForkPluginPack, MockForkPluginPackWithoutPath])
		.mockRejectedValue({
			...mockApiError({ message: "Forbidden." }),
			status: 403,
		});
	const { queryClient } = render(
		<ForkPluginPackToggles
			target={target}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);
	await screen.findByRole("switch", {
		name: `Enable ${MockForkPluginPack.name}`,
	});

	await queryClient.invalidateQueries({
		queryKey: pluginPacksKey(MockForkPluginPack.organization_id),
	});

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2),
	);
	await user.click(
		screen.getByRole("switch", {
			name: `Enable ${MockForkPluginPackWithoutPath.name}`,
		}),
	);

	await waitFor(() =>
		expect(enablementApi.setPluginPackEnabled).toHaveBeenCalledWith(
			target,
			MockForkPluginPackWithoutPath.id,
			false,
		),
	);
});

const toggleDeletedPack = async (
	toggleTarget: enablementApi.PluginPackTarget,
) => {
	const user = userEvent.setup();
	vi.spyOn(forkPluginPacksApi, "getPluginPacks").mockResolvedValue([
		MockForkPluginPack,
	]);
	vi.mocked(enablementApi.setPluginPackEnabled).mockRejectedValueOnce({
		...mockApiError({ message: "Resource not found." }),
		status: 404,
	});
	render(
		<ForkPluginPackToggles
			target={toggleTarget}
			organizationId={MockForkPluginPack.organization_id}
			canEdit
		/>,
	);

	await user.click(
		await screen.findByRole("switch", {
			name: `Enable ${MockForkPluginPack.name}`,
		}),
	);
};

it("refetches the installed packs when a chat toggle finds a deleted pack", async () => {
	await toggleDeletedPack(target);

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2),
	);
});

it("refetches the installed packs when a project toggle finds a deleted pack", async () => {
	await toggleDeletedPack({
		kind: "project",
		organizationId: MockForkPluginPack.organization_id,
		projectId: "project-1",
	});

	await waitFor(() =>
		expect(forkPluginPacksApi.getPluginPacks).toHaveBeenCalledTimes(2),
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
