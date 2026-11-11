import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import type { Chat, Experiment, ForkAgentType } from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockUserMember,
	MockWorkspace,
	mockApiError,
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

type RenderPageOptions = {
	experiments?: Experiment[];
	getChats?: () => Promise<Chat[]>;
	getAgentTypes?: () => Promise<ForkAgentType[]>;
};

const renderPage = ({
	experiments = ["fork-agent-definitions"],
	getChats = async () => [
		mockDefaultRootChat,
		mockExploreChat,
		mockOtherOrgReviewerChat,
	],
	getAgentTypes = async () => [mockReviewer],
}: RenderPageOptions = {}) => {
	vi.spyOn(API, "getExperiments").mockResolvedValue(experiments);
	vi.spyOn(API, "getAuthenticatedUser").mockResolvedValue({
		...MockUserMember,
		organization_ids: [MockDefaultOrganization.id, MockOrganization2.id],
	});
	vi.spyOn(API, "getOrganizations").mockResolvedValue([
		MockDefaultOrganization,
		MockOrganization2,
	]);
	vi.spyOn(API.experimental, "getChatProjects").mockResolvedValue([]);
	vi.spyOn(API, "getWorkspaces").mockResolvedValue({
		workspaces: [mockWorkspaceDev],
		count: 1,
	});
	vi.spyOn(API.experimental, "getChats").mockImplementation(getChats);
	const getAgentTypesSpy = vi
		.spyOn(forkAgentDefinitionsApi, "getAgentTypes")
		.mockImplementation(getAgentTypes);
	const { router } = renderWithAuth(<ForkAvailableAgentsPage />, {
		path: "/agents/settings/available-agents",
		route: "/agents/settings/available-agents",
		extraRoutes: [
			{ path: "/agents/settings", element: <div /> },
			{ path: "/agents/:agentId", element: <div /> },
		],
	});
	return { getAgentTypes: getAgentTypesSpy, router };
};

afterEach(() => {
	vi.useRealTimers();
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

	it("shows a failed chat list instead of claiming no chats", async () => {
		// The sidebar's chat list retries with backoff before it fails.
		vi.useFakeTimers({ shouldAdvanceTime: true });
		renderPage({
			getChats: () =>
				Promise.reject(mockApiError({ message: "Failed to load chats." })),
		});

		const row = within(
			await screen.findByRole("row", { name: /personal\/reviewer/ }),
		);
		await vi.advanceTimersByTimeAsync(10_000);
		expect(screen.getByText("Failed to load chats.")).toBeInTheDocument();
		expect(row.queryByText("None")).not.toBeInTheDocument();
	});

	it("adds the picked workspace's agents", async () => {
		const { getAgentTypes } = renderPage();
		const user = userEvent.setup();

		await screen.findByText("personal/reviewer");
		await user.click(
			screen.getByRole("combobox", { name: "Workspace: No workspace" }),
		);
		await user.click(await screen.findByRole("option", { name: "dev" }));

		await waitFor(() =>
			expect(getAgentTypes).toHaveBeenLastCalledWith(
				MockDefaultOrganization.id,
				{ project: undefined, workspace: mockWorkspaceDev.id },
			),
		);
	});

	it("forgets the picked workspace when the organization changes", async () => {
		renderPage();
		const user = userEvent.setup();
		const pickOrganization = async (from: string, to: string) => {
			await user.click(
				screen.getByRole("combobox", { name: `Organization ${from}` }),
			);
			// The option's name starts with the avatar's initials.
			await user.click(
				screen.getByRole("option", { name: new RegExp(`${to}$`) }),
			);
		};

		await screen.findByText("personal/reviewer");
		await user.click(
			screen.getByRole("combobox", { name: "Workspace: No workspace" }),
		);
		await user.click(await screen.findByRole("option", { name: "dev" }));
		await screen.findByRole("combobox", { name: "Workspace: dev" });
		await pickOrganization(
			MockDefaultOrganization.display_name,
			MockOrganization2.display_name,
		);
		await pickOrganization(
			MockOrganization2.display_name,
			MockDefaultOrganization.display_name,
		);

		expect(
			await screen.findByRole("combobox", { name: "Workspace: No workspace" }),
		).toBeVisible();
	});

	it("shows only the error when the agent types fail to load", async () => {
		renderPage({
			getAgentTypes: () =>
				Promise.reject(mockApiError({ message: "Failed to load agents." })),
		});

		await screen.findByText("Failed to load agents.");
		expect(
			screen.queryByText("No agents are available."),
		).not.toBeInTheDocument();
	});

	it("redirects to the settings page without the experiment", async () => {
		const { getAgentTypes, router } = renderPage({ experiments: [] });

		await waitFor(() =>
			expect(router.state.location.pathname).toBe("/agents/settings"),
		);
		expect(getAgentTypes).not.toHaveBeenCalled();
	});
});
