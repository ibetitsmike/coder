import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import type { Chat, Experiment, ForkAgentType } from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockDefaultOrganization,
	MockUserMember,
	MockWorkspace,
} from "#/testHelpers/entities";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import ForkAvailableAgentsPage from "./ForkAvailableAgentsPage";

const reviewer: ForkAgentType = {
	name: "personal/reviewer",
	source: "personal",
	description: "Reviews the diff.",
	base_type: "general",
	root_available: false,
	root_unavailable_reason: "Its model is not available.",
};

const chat = (id: string, overrides: Partial<Chat> = {}): Chat => ({
	...MockChat,
	id,
	title: `Chat ${id}`,
	organization_id: MockDefaultOrganization.id,
	agent_type: "reviewer",
	...overrides,
});

const workspace = {
	...MockWorkspace,
	id: "ws-1",
	name: "dev",
	organization_id: MockDefaultOrganization.id,
};

const renderPage = (experiments: Experiment[] = ["fork-agent-definitions"]) => {
	vi.spyOn(API, "getExperiments").mockResolvedValue(experiments);
	vi.spyOn(API, "getAuthenticatedUser").mockResolvedValue({
		...MockUserMember,
		organization_ids: [MockDefaultOrganization.id],
	});
	vi.spyOn(API, "getOrganizations").mockResolvedValue([
		MockDefaultOrganization,
	]);
	vi.spyOn(API.experimental, "getChatProjects").mockResolvedValue([]);
	vi.spyOn(API, "getWorkspaces").mockResolvedValue({
		workspaces: [workspace],
		count: 1,
	});
	vi.spyOn(API.experimental, "getChats").mockResolvedValue([
		chat("root", {
			agent_type: undefined,
			children: [chat("child")],
		}),
		chat("other-type", { agent_type: "explore" }),
		chat("other-org", { organization_id: "another-org" }),
	]);
	const getAgentTypes = vi
		.spyOn(forkAgentDefinitionsApi, "getAgentTypes")
		.mockResolvedValue([reviewer]);
	const { router } = renderWithAuth(<ForkAvailableAgentsPage />, {
		path: "/agents/settings/available-agents",
		route: "/agents/settings/available-agents",
		extraRoutes: [
			{ path: "/agents/settings", element: <div /> },
			{ path: "/agents/:agentId", element: <div /> },
		],
	});
	return { getAgentTypes, router };
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkAvailableAgentsPage", () => {
	it("lists each agent with its tier, availability, and recent chats", async () => {
		const { router } = renderPage();
		const user = userEvent.setup();

		const row = within(
			(await screen.findByText("personal/reviewer")).closest("tr")!,
		);
		expect(row.getByText("personal")).toBeInTheDocument();
		expect(row.getByText("Its model is not available.")).toBeInTheDocument();
		const links = await row.findAllByRole("link");
		expect(links.map((link) => link.textContent)).toEqual(["Chat child"]);
		await user.click(row.getByRole("link", { name: "Chat child" }));

		expect(router.state.location.pathname).toBe("/agents/child");
	});

	it("adds the picked workspace's agents", async () => {
		const { getAgentTypes } = renderPage();
		const user = userEvent.setup();

		await screen.findByText("personal/reviewer");
		await user.click(screen.getByRole("combobox", { name: "Workspace" }));
		await user.click(await screen.findByRole("option", { name: "dev" }));

		await waitFor(() =>
			expect(getAgentTypes).toHaveBeenLastCalledWith(
				MockDefaultOrganization.id,
				{ project: undefined, workspace: workspace.id },
			),
		);
	});

	it("redirects to the settings page without the experiment", async () => {
		const { getAgentTypes, router } = renderPage([]);

		await waitFor(() =>
			expect(router.state.location.pathname).toBe("/agents/settings"),
		);
		expect(getAgentTypes).not.toHaveBeenCalled();
	});
});
