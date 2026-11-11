import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { QueryClientProvider } from "react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import {
	afterAll,
	afterEach,
	beforeAll,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { API } from "#/api/api";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import { chat } from "#/api/queries/chats";
import { agentDefinitions } from "#/api/queries/forkAgentDefinitions";
import { userSkills } from "#/api/queries/userSkills";
import type { Chat, Experiment } from "#/api/typesGenerated";
import {
	DashboardContext,
	type DashboardValue,
} from "#/modules/dashboard/DashboardProvider";
import {
	MockChat,
	MockChatContextAgentDefinitions,
} from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { createTestQueryClient } from "#/testHelpers/renderHelpers";
import { MockSkill } from "#/testHelpers/skills";
import { DEFAULT_AGENT_CHAT_SEND_SHORTCUT } from "../../utils/agentChatSendShortcut";
import { ChatMessageInput, type ChatMessageInputRef } from "./ChatMessageInput";

// jsdom has no layout; the typeahead reads the caret's rectangle.
const rangeRect = Object.getOwnPropertyDescriptor(
	Range.prototype,
	"getBoundingClientRect",
);
beforeAll(() => {
	Object.defineProperty(Range.prototype, "getBoundingClientRect", {
		configurable: true,
		value: () => new DOMRect(0, 0, 1, 16),
	});
});
afterAll(() => {
	if (rangeRect) {
		Object.defineProperty(Range.prototype, "getBoundingClientRect", rangeRect);
	} else {
		Reflect.deleteProperty(Range.prototype, "getBoundingClientRect");
	}
});

afterEach(() => vi.restoreAllMocks());

const renderComposer = ({
	chat: chatOverrides,
	experiments = [],
}: {
	chat?: Partial<Chat>;
	experiments?: Experiment[];
} = {}) => {
	const getPersonal = vi
		.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinitions")
		.mockResolvedValue([
			{ ...MockForkAgentDefinitionMetadata, name: "reviewer" },
		]);
	vi.spyOn(
		forkAgentDefinitionsApi,
		"getOrganizationAgentDefinitions",
	).mockResolvedValue([]);
	const queryClient = createTestQueryClient();
	queryClient.setQueryData(userSkills().queryKey, [MockSkill]);
	queryClient.setQueryData(chat(MockChat.id).queryKey, {
		...MockChat,
		...chatOverrides,
	});
	const inputRef = createRef<ChatMessageInputRef>();
	const onEnter = vi.fn();
	const dashboard: DashboardValue = {
		entitlements: MockEntitlements,
		experiments: ["fork-agent-definitions", ...experiments],
		appearance: MockAppearanceConfig,
		buildInfo: MockBuildInfo,
		organizations: [MockDefaultOrganization],
		showOrganizations: false,
		canViewOrganizationSettings: false,
	};
	render(
		<QueryClientProvider client={queryClient}>
			<DashboardContext.Provider value={dashboard}>
				<MemoryRouter initialEntries={[`/agents/${MockChat.id}`]}>
					<Routes>
						<Route
							path="/agents/:agentId"
							element={
								<ChatMessageInput
									ref={inputRef}
									aria-label="Chat message input"
									placeholder=""
									initialValue=""
									onChange={vi.fn()}
									onEnter={onEnter}
									sendShortcut={DEFAULT_AGENT_CHAT_SEND_SHORTCUT}
									disabled={false}
									hasWorkspace={false}
									organizationId={MockDefaultOrganization.id}
								/>
							}
						/>
					</Routes>
				</MemoryRouter>
			</DashboardContext.Provider>
		</QueryClientProvider>,
	);
	return { inputRef, onEnter, getPersonal, queryClient };
};

const typeIntoComposer = async (text: string) => {
	const user = userEvent.setup();
	await user.click(screen.getByRole("textbox", { name: "Chat message input" }));
	await user.paste(text);
	return user;
};

describe("ForkAgentMentionPlugin", () => {
	it("replaces the mention with a delegation to the chosen built-in type", async () => {
		const { inputRef } = renderComposer();
		const user = await typeIntoComposer("Please @exp");
		await screen.findByRole("option", { name: /^@explore/ });
		await user.keyboard("{Enter}trace the login flow");
		expect(inputRef.current?.getValue()).toBe(
			"Please Delegate to the explore agent (spawn_agent type explore): trace the login flow",
		);
	});

	it("ignores an @ inside a word and loads definitions only once the menu opens", async () => {
		const { onEnter, getPersonal } = renderComposer();
		const user = await typeIntoComposer("mail me@exp");
		await user.keyboard("{Enter}");
		expect(onEnter).toHaveBeenCalledTimes(1);
		expect(getPersonal).not.toHaveBeenCalled();
		await user.keyboard(" @");
		await waitFor(() => expect(getPersonal).toHaveBeenCalledTimes(1));
	});

	it("qualifies a name that a stored and a workspace source both define", async () => {
		const { inputRef } = renderComposer({
			chat: { context: MockChatContextAgentDefinitions },
		});
		const user = await typeIntoComposer("@rev");
		await user.click(
			await screen.findByRole("option", { name: /^@workspace\/reviewer/ }),
		);
		expect(inputRef.current?.getValue()).toBe(
			"Delegate to the workspace/reviewer agent (spawn_agent type workspace/reviewer): ",
		);
	});

	it("inserts the option the arrow keys highlight", async () => {
		const { inputRef } = renderComposer();
		const user = await typeIntoComposer("@");
		await screen.findByRole("option", { name: /^@explore/ });
		await user.keyboard("{ArrowDown}{Enter}");
		expect(inputRef.current?.getValue()).toBe(
			"Delegate to the explore agent (spawn_agent type explore): ",
		);
	});

	it("qualifies stored names while the workspace agents are unknown", async () => {
		const { inputRef } = renderComposer({
			chat: { context: { dirty: false } },
		});
		const user = await typeIntoComposer("@rev");
		await user.click(
			await screen.findByRole("option", { name: /^@personal\/reviewer/ }),
		);
		expect(inputRef.current?.getValue()).toBe(
			"Delegate to the personal/reviewer agent (spawn_agent type personal/reviewer): ",
		);
	});

	it("keeps cached agents usable when a refetch fails", async () => {
		const { getPersonal, queryClient, inputRef } = renderComposer();
		queryClient.setQueryData(
			agentDefinitions({
				tier: "user",
				organizationId: MockDefaultOrganization.id,
			}).queryKey,
			[{ ...MockForkAgentDefinitionMetadata, name: "cached" }],
			{ updatedAt: 0 },
		);
		getPersonal.mockRejectedValue(new Error("offline"));
		const user = await typeIntoComposer("@");
		await screen.findByText(
			"Could not load some agents. Close and type @ again to retry.",
		);
		await user.click(screen.getByRole("option", { name: /^@cached/ }));
		expect(inputRef.current?.getValue()).toBe(
			"Delegate to the cached agent (spawn_agent type cached): ",
		);
	});

	it("keeps Enter from sending after agents fail to load until the menu closes", async () => {
		const { onEnter, getPersonal } = renderComposer();
		getPersonal.mockRejectedValue(new Error("offline"));
		const user = await typeIntoComposer("@rev");
		await screen.findByText(
			"Could not load some agents. Close and type @ again to retry.",
		);
		await user.keyboard("{Enter}");
		expect(onEnter).not.toHaveBeenCalled();
		await user.keyboard("{Escape}{Enter}");
		expect(onEnter).toHaveBeenCalledTimes(1);
	});

	it("keeps Enter from sending while agents load", async () => {
		const { onEnter, getPersonal } = renderComposer();
		getPersonal.mockReturnValue(new Promise(() => {}));
		const user = await typeIntoComposer("@rev");
		await screen.findByText("Loading agents...");
		await user.keyboard("{Enter}");
		expect(onEnter).not.toHaveBeenCalled();
	});

	it.each([
		{ agent_type: "general", experiments: [] },
		{ agent_type: "reviewer", experiments: ["fork-nested-subagents" as const] },
	])(
		"leaves @ as text in a $agent_type child chat that cannot spawn agents",
		async ({ agent_type, experiments }) => {
			const { onEnter } = renderComposer({
				chat: { parent_chat_id: "parent", root_chat_id: "root", agent_type },
				experiments,
			});
			const user = await typeIntoComposer("@exp");
			await user.keyboard("{Enter}");
			expect(onEnter).toHaveBeenCalledTimes(1);
		},
	);

	it("offers a nested child chat its root chat's project agents", async () => {
		const getChat = vi
			.spyOn(API.experimental, "getChat")
			.mockResolvedValue({ ...MockChat, id: "root", project_id: "project" });
		const getProject = vi
			.spyOn(forkAgentDefinitionsApi, "getProjectAgentDefinitions")
			.mockResolvedValue([
				{ ...MockForkAgentDefinitionMetadata, name: "deployer" },
			]);
		renderComposer({
			chat: {
				parent_chat_id: "root",
				root_chat_id: "root",
				agent_type: "general",
			},
			experiments: ["fork-nested-subagents"],
		});
		await typeIntoComposer("@dep");
		await screen.findByRole("option", { name: /^@deployer/ });
		expect(getChat).toHaveBeenCalledWith("root", expect.anything());
		expect(getProject).toHaveBeenCalledWith(
			MockDefaultOrganization.id,
			"project",
		);
	});

	it("keeps / opening the skills menu", async () => {
		const { inputRef } = renderComposer();
		const user = await typeIntoComposer("/ski");
		await user.click(
			await screen.findByRole("option", { name: /^\/personal\/skill/ }),
		);
		expect(inputRef.current?.getContentParts()).toEqual([
			{ type: "skill-invocation", source: "personal", name: MockSkill.name },
			{ type: "text", text: " " },
		]);
	});
});
