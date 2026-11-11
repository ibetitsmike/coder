import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import type { Experiment, ForkAgentType } from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockChatModelProviderDescriptor,
	MockDefaultChatModel,
	MockUnsetUserChatPersonalModelOverrides,
} from "#/testHelpers/chatModels";
import { MockUserPreferenceSettings } from "#/testHelpers/entities";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import AgentCreatePage from "../AgentCreatePage/AgentCreatePage";

// AgentPageHeader needs the layout's outlet context.
vi.mock("./AgentPageHeader", () => ({ AgentPageHeader: () => null }));

const agentType = (name: string, source: string, reason?: string) => ({
	name,
	source,
	description: `The ${name} agent.`,
	base_type: "general",
	root_available: reason === undefined,
	root_unavailable_reason: reason,
});

const agentTypes: ForkAgentType[] = [
	agentType("general", "builtin"),
	agentType("reviewer", "personal"),
	agentType("deployer", "org", "Its model is not available."),
];

const renderPage = (experiments: Experiment[]) => {
	vi.spyOn(API, "getExperiments").mockResolvedValue(experiments);
	vi.spyOn(API.experimental, "getChatProjects").mockResolvedValue([]);
	vi.spyOn(API.experimental, "getChatModels").mockResolvedValue({
		models: [MockDefaultChatModel],
		providers: [MockChatModelProviderDescriptor],
		unsupported_providers: [],
	});
	vi.spyOn(
		API.experimental,
		"getUserChatPersonalModelOverrides",
	).mockResolvedValue(MockUnsetUserChatPersonalModelOverrides);
	vi.spyOn(API.experimental, "getMCPServerConfigs").mockResolvedValue([]);
	vi.spyOn(API, "getAIProviders").mockResolvedValue([]);
	vi.spyOn(API, "getUserPreferenceSettings").mockResolvedValue(
		MockUserPreferenceSettings,
	);
	const getAgentTypes = vi
		.spyOn(forkAgentDefinitionsApi, "getAgentTypes")
		.mockResolvedValue(agentTypes);
	const createChat = vi
		.spyOn(API.experimental, "createChat")
		.mockResolvedValue({ ...MockChat, id: "new-chat-id" });
	renderWithAuth(<AgentCreatePage />, {
		path: "/agents",
		route: "/agents",
		extraRoutes: [{ path: "/agents/:agentId", element: null }],
	});
	return { getAgentTypes, createChat };
};

const send = async (user: ReturnType<typeof userEvent.setup>) => {
	await user.click(
		await screen.findByRole("textbox", { name: "Chat message" }),
	);
	await user.paste("Review my change");
	const sendButton = await screen.findByRole("button", { name: "Send" });
	await waitFor(() => expect(sendButton).toBeEnabled());
	await user.click(sendButton);
};

// Lexical reads selection geometry when text is pasted; jsdom has none.
beforeAll(() => {
	Object.defineProperty(Range.prototype, "getBoundingClientRect", {
		configurable: true,
		value: () => new DOMRect(0, 0, 1, 16),
	});
});

afterEach(() => {
	vi.restoreAllMocks();
	localStorage.clear();
});

describe("useForkAgentTypePicker", () => {
	it("creates the chat as the picked type and skips unavailable rows", async () => {
		const { createChat } = renderPage(["fork-agent-definitions"]);
		// The disabled row has pointer-events: none; click it anyway.
		const user = userEvent.setup({ pointerEventsCheck: 0 });

		await user.click(
			await screen.findByRole("button", { name: "Agent type: Default agent" }),
		);
		const deployer = await screen.findByRole("option", { name: /deployer/ });
		expect(deployer).toHaveAttribute("aria-disabled", "true");
		expect(deployer).toHaveTextContent("Its model is not available.");
		// Selecting a row closes the menu, so the next row is only there to
		// click when the unavailable one was not selected.
		await user.click(deployer);
		await user.click(screen.getByRole("option", { name: /reviewer/ }));
		await send(user);

		await waitFor(() => expect(createChat).toHaveBeenCalledTimes(1));
		expect(createChat.mock.calls[0][0]).toMatchObject({
			agent_type: "reviewer",
		});
	});

	it("has no picker without the experiment", async () => {
		const { createChat, getAgentTypes } = renderPage([]);
		const user = userEvent.setup();

		await send(user);

		await waitFor(() => expect(createChat).toHaveBeenCalledTimes(1));
		expect(
			screen.queryByRole("button", { name: /^Agent type:/ }),
		).not.toBeInTheDocument();
		expect(getAgentTypes).not.toHaveBeenCalled();
	});
});
