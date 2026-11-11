import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import * as enablementApi from "#/api/forkPluginPackEnablement";
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

it("enables an installed pack for the target", async () => {
	const user = userEvent.setup();
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
