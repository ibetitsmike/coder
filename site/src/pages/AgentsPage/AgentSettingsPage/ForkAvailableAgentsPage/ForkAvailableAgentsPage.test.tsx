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

const mockReviewer: ForkAgentType = {
	name: "personal/reviewer",
	source: "personal",
	description: "Reviews the diff.",
	base_type: "general",
	root_available: false,
	root_unavailable_reason: "Its model is not available.",
};

const mockReviewerChildChat: Chat = {
	...MockChat,
	id: "child",
	title: "Chat child",
	organization_id: MockDefaultOrganization.id,
	agent_type: "reviewer",
};

const mockDefaultRootChat: Chat = {
	...mockReviewerChildChat,
	id: "root",
	title: "Chat root",
	agent_type: undefined,
	children: [mockReviewerChildChat],
};

const mockExploreChat: Chat = {
	...mockReviewerChildChat,
	id: "other-type",
	title: "Chat other-type",
	agent_type: "explore",
};

const mockOtherOrgReviewerChat: Chat = {
	...mockReviewerChildChat,
	id: "other-org",
	title: "Chat other-org",
	organization_id: "another-org",
};

const mockWorkspaceDev = {
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
		workspaces: [mockWorkspaceDev],
		count: 1,
	});
	vi.spyOn(API.experimental, "getChats").mockResolvedValue([
		mockDefaultRootChat,
		mockExploreChat,
		mockOtherOrgReviewerChat,
	]);
	const getAgentTypes = vi
		.spyOn(forkAgentDefinitionsApi, "getAgentTypes")
		.mockResolvedValue([mockReviewer]);
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
	it("links each agent's recent chats", async () => {
		const { router } = renderPage();
		const user = userEvent.setup();

		const row = within(
			await screen.findByRole("row", { name: /personal\/reviewer/ }),
		);
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
				{ project: undefined, workspace: mockWorkspaceDev.id },
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
