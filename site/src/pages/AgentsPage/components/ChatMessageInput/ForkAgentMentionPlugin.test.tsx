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
import * as forkSavedWorkflowsApi from "#/api/forkSavedWorkflows";
import { chat } from "#/api/queries/chats";
import { agentDefinitions } from "#/api/queries/forkAgentDefinitions";
import {
	savedWorkflows,
	savedWorkflowsKey,
} from "#/api/queries/forkSavedWorkflows";
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
import {
	MockForkOrganizationSavedWorkflowMetadata,
	MockForkSavedWorkflowMetadata,
} from "#/testHelpers/forkSavedWorkflows";
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
	isNewChat = false,
	hasWorkspace = false,
	skipAgentDefinitionsExperiment = false,
	isPlanMode = false,
	isExploreAgentType = false,
}: {
	chat?: Partial<Chat>;
	experiments?: Experiment[];
	isNewChat?: boolean;
	hasWorkspace?: boolean;
	skipAgentDefinitionsExperiment?: boolean;
	isPlanMode?: boolean;
	isExploreAgentType?: boolean;
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
	const getUserWorkflows = vi
		.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflows")
		.mockResolvedValue([]);
	const getOrganizationWorkflows = vi
		.spyOn(forkSavedWorkflowsApi, "getOrganizationSavedWorkflows")
		.mockResolvedValue([]);
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
		experiments: [
			...(skipAgentDefinitionsExperiment
				? []
				: (["fork-agent-definitions"] as const)),
			...experiments,
		],
		appearance: MockAppearanceConfig,
		buildInfo: MockBuildInfo,
		organizations: [MockDefaultOrganization],
		showOrganizations: false,
		canViewOrganizationSettings: false,
	};
	render(
		<QueryClientProvider client={queryClient}>
			<DashboardContext.Provider value={dashboard}>
				<MemoryRouter
					initialEntries={[isNewChat ? "/agents" : `/agents/${MockChat.id}`]}
				>
					<Routes>
						<Route
							path={isNewChat ? "/agents" : "/agents/:agentId"}
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
									hasWorkspace={hasWorkspace}
									isPlanMode={isPlanMode}
									isExploreAgentType={isExploreAgentType}
									organizationId={MockDefaultOrganization.id}
								/>
							}
						/>
					</Routes>
				</MemoryRouter>
			</DashboardContext.Provider>
		</QueryClientProvider>,
	);
	return {
		inputRef,
		onEnter,
		getPersonal,
		getUserWorkflows,
		getOrganizationWorkflows,
		queryClient,
	};
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

	it("matches a tier-qualified name after the agents load", async () => {
		const { inputRef } = renderComposer();
		const user = await typeIntoComposer("@personal/rev");
		await user.click(await screen.findByRole("option", { name: /^@reviewer/ }));
		expect(inputRef.current?.getValue()).toBe(
			"Delegate to the reviewer agent (spawn_agent type reviewer): ",
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

	it("qualifies stored names in a new chat with a selected workspace", async () => {
		const { inputRef } = renderComposer({
			isNewChat: true,
			hasWorkspace: true,
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
		const userKey = agentDefinitions({
			tier: "user",
			organizationId: MockDefaultOrganization.id,
		}).queryKey;
		queryClient.setQueryData(
			userKey,
			[{ ...MockForkAgentDefinitionMetadata, name: "cached" }],
			{ updatedAt: 0 },
		);
		getPersonal.mockRejectedValue(new Error("offline"));
		const user = await typeIntoComposer("@");
		await waitFor(() =>
			expect(queryClient.getQueryState(userKey)?.status).toBe("error"),
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

	it("retries failed agents when an edit reopens the closed menu", async () => {
		const { getPersonal, inputRef } = renderComposer();
		getPersonal.mockRejectedValueOnce(new Error("offline"));
		const user = await typeIntoComposer("@rev");
		await screen.findByText(
			"Could not load some agents. Close and type @ again to retry.",
		);
		await user.keyboard("{Escape}i");
		await user.click(await screen.findByRole("option", { name: /^@reviewer/ }));
		expect(getPersonal).toHaveBeenCalledTimes(2);
		expect(inputRef.current?.getValue()).toBe(
			"Delegate to the reviewer agent (spawn_agent type reviewer): ",
		);
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

	it("keeps Enter from sending the mention while a workflow load failed", async () => {
		const { getUserWorkflows, onEnter } = renderComposer({
			experiments: ["fork-workflows"],
		});
		getUserWorkflows.mockRejectedValue(new Error("offline"));
		// The query matches no agent, so only the failed workflow load can
		// hold Enter back.
		const user = await typeIntoComposer("@zzz");
		await screen.findByText(
			"Could not load some workflows. Close and type @ again to retry.",
		);
		await user.keyboard("{Enter}");
		expect(onEnter).not.toHaveBeenCalled();
	});

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

	it("offers the saved workflows of both tiers in a Workflows group", async () => {
		const { getUserWorkflows, getOrganizationWorkflows, inputRef } =
			renderComposer({ experiments: ["fork-workflows"] });
		getUserWorkflows.mockResolvedValue([MockForkSavedWorkflowMetadata]);
		getOrganizationWorkflows.mockResolvedValue([
			MockForkOrganizationSavedWorkflowMetadata,
		]);
		const user = await typeIntoComposer("@");
		await screen.findByRole("option", { name: /^@explore/ });
		await screen.findByText("Workflows");
		await user.click(
			screen.getByRole("option", {
				name: new RegExp(`^@${MockForkSavedWorkflowMetadata.name}`),
			}),
		);
		expect(inputRef.current?.getValue()).toBe(
			`Run the ${MockForkSavedWorkflowMetadata.name} saved workflow (run_workflow name ${MockForkSavedWorkflowMetadata.name}), with args: files (required). `,
		);
		expect(getUserWorkflows).toHaveBeenCalledWith(MockDefaultOrganization.id);
		expect(getOrganizationWorkflows).toHaveBeenCalledWith(
			MockDefaultOrganization.id,
		);
	});

	it("shows a workflow without an args schema as taking no args", async () => {
		const { getUserWorkflows, inputRef } = renderComposer({
			experiments: ["fork-workflows"],
		});
		getUserWorkflows.mockResolvedValue([
			MockForkOrganizationSavedWorkflowMetadata,
		]);
		const user = await typeIntoComposer("@rot");
		await user.click(
			await screen.findByRole("option", {
				name: new RegExp(`^@${MockForkOrganizationSavedWorkflowMetadata.name}`),
			}),
		);
		expect(inputRef.current?.getValue()).toBe(
			`Run the ${MockForkOrganizationSavedWorkflowMetadata.name} saved workflow (run_workflow name ${MockForkOrganizationSavedWorkflowMetadata.name}): `,
		);
	});

	it("hides an organization workflow that a personal one hides", async () => {
		const { getUserWorkflows, getOrganizationWorkflows, inputRef } =
			renderComposer({
				experiments: ["fork-workflows"],
			});
		getUserWorkflows.mockResolvedValue([
			{
				...MockForkSavedWorkflowMetadata,
				name: MockForkOrganizationSavedWorkflowMetadata.name,
			},
		]);
		getOrganizationWorkflows.mockResolvedValue([
			MockForkOrganizationSavedWorkflowMetadata,
		]);
		const user = await typeIntoComposer("@");
		await user.click(
			await screen.findByRole("option", {
				name: new RegExp(
					`^@${MockForkOrganizationSavedWorkflowMetadata.name}\\s*personal`,
				),
			}),
		);
		// The click must run the personal tier the way run_workflow resolves
		// the name, so the composer holds the personal workflow's prompt, not
		// the org workflow's.
		expect(inputRef.current?.getValue()).toBe(
			`Run the ${MockForkOrganizationSavedWorkflowMetadata.name} saved workflow (run_workflow name ${MockForkOrganizationSavedWorkflowMetadata.name}), with args: files (required). `,
		);
	});

	it("hides a workflow whose name an agent mention already takes", async () => {
		const { getUserWorkflows, inputRef } = renderComposer({
			experiments: ["fork-workflows"],
		});
		// explore is a built-in agent type, so the workflow would otherwise
		// produce a second @explore option that runs instead of delegates.
		getUserWorkflows.mockResolvedValue([
			{ ...MockForkSavedWorkflowMetadata, name: "explore" },
		]);
		const user = await typeIntoComposer("@");
		await user.click(await screen.findByRole("option", { name: /^@explore/ }));
		// The one @explore option must delegate to the built-in type, not run
		// the workflow that took its name.
		expect(inputRef.current?.getValue()).toBe(
			"Delegate to the explore agent (spawn_agent type explore): ",
		);
	});

	it("hides a workflow whose name a reserved agent type takes", async () => {
		const { getUserWorkflows, inputRef } = renderComposer({
			experiments: ["fork-workflows"],
		});
		// chatd names the reserved types for spawn_agent only, so run_workflow
		// could never resolve this workflow's name; the option would run the
		// workflow's definition through a delegation instead.
		getUserWorkflows.mockResolvedValue([
			{ ...MockForkSavedWorkflowMetadata, name: "general" },
		]);
		const user = await typeIntoComposer("@");
		await user.click(await screen.findByRole("option", { name: /^@general/ }));
		// The one @general option must delegate to the built-in type, which
		// is the only resolution run_workflow could never be asked for.
		expect(inputRef.current?.getValue()).toBe(
			"Delegate to the general agent (spawn_agent type general): ",
		);
	});

	it("summarizes a schema that constrains args through required alone", async () => {
		const { getUserWorkflows, inputRef } = renderComposer({
			experiments: ["fork-workflows"],
		});
		getUserWorkflows.mockResolvedValue([
			{
				...MockForkSavedWorkflowMetadata,
				args_schema: { type: "object", required: ["topic"] },
			},
		]);
		const user = await typeIntoComposer("@");
		await user.click(
			await screen.findByRole("option", {
				name: new RegExp(`^@${MockForkSavedWorkflowMetadata.name}`),
			}),
		);
		expect(inputRef.current?.getValue()).toBe(
			`Run the ${MockForkSavedWorkflowMetadata.name} saved workflow (run_workflow name ${MockForkSavedWorkflowMetadata.name}), with args: topic (required). `,
		);
	});

	it("summarizes required names that properties does not list", async () => {
		const { getUserWorkflows, inputRef } = renderComposer({
			experiments: ["fork-workflows"],
		});
		getUserWorkflows.mockResolvedValue([
			{
				...MockForkSavedWorkflowMetadata,
				args_schema: {
					type: "object",
					properties: { note: { type: "string" } },
					required: ["topic"],
				},
			},
		]);
		const user = await typeIntoComposer("@");
		await user.click(
			await screen.findByRole("option", {
				name: new RegExp(`^@${MockForkSavedWorkflowMetadata.name}`),
			}),
		);
		expect(inputRef.current?.getValue()).toBe(
			`Run the ${MockForkSavedWorkflowMetadata.name} saved workflow (run_workflow name ${MockForkSavedWorkflowMetadata.name}), with args: note, topic (required). `,
		);
	});

	it("filters the workflows by the typed text", async () => {
		const { getUserWorkflows, inputRef } = renderComposer({
			experiments: ["fork-workflows"],
		});
		getUserWorkflows.mockResolvedValue([
			MockForkSavedWorkflowMetadata,
			MockForkOrganizationSavedWorkflowMetadata,
		]);
		const user = await typeIntoComposer("@rot");
		await screen.findByRole("option", {
			name: new RegExp(`^@${MockForkOrganizationSavedWorkflowMetadata.name}`),
		});
		expect(
			screen.queryByRole("option", {
				name: new RegExp(`^@${MockForkSavedWorkflowMetadata.name}`),
			}),
		).toBeNull();
		// The filtered option must be the one Enter inserts, so the composer
		// value proves the filtering fed the selectable result.
		await user.keyboard("{Enter}");
		expect(inputRef.current?.getValue()).toBe(
			`Run the ${MockForkOrganizationSavedWorkflowMetadata.name} saved workflow (run_workflow name ${MockForkOrganizationSavedWorkflowMetadata.name}): `,
		);
		// Reopening with an empty query restores every workflow.
		await typeIntoComposer(" @");
		await screen.findByRole("option", {
			name: new RegExp(`^@${MockForkSavedWorkflowMetadata.name}`),
		});
		await screen.findByRole("option", {
			name: new RegExp(`^@${MockForkOrganizationSavedWorkflowMetadata.name}`),
		});
	});

	it("inserts the workflow the arrow keys highlight", async () => {
		const { getUserWorkflows, inputRef } = renderComposer({
			experiments: ["fork-workflows"],
		});
		getUserWorkflows.mockResolvedValue([
			MockForkOrganizationSavedWorkflowMetadata,
		]);
		const user = await typeIntoComposer("@");
		await screen.findByRole("option", {
			name: new RegExp(`^@${MockForkOrganizationSavedWorkflowMetadata.name}`),
		});
		await user.keyboard("{ArrowDown}{ArrowDown}{ArrowDown}{Enter}");
		expect(inputRef.current?.getValue()).toBe(
			`Run the ${MockForkOrganizationSavedWorkflowMetadata.name} saved workflow (run_workflow name ${MockForkOrganizationSavedWorkflowMetadata.name}): `,
		);
	});

	it.each([
		{ label: "fork-workflows off", experiments: [] as Experiment[] },
		{
			label: "a child chat",
			// A general child chat still opens the agent menu, so this case
			// proves the gate rather than a missing menu.
			experiments: ["fork-workflows", "fork-nested-subagents"] as Experiment[],
			chat: {
				parent_chat_id: "parent",
				root_chat_id: "root",
				agent_type: "general",
			},
		},
		{
			label: "plan mode",
			// The composer stays open in plan mode, so this proves the gate.
			experiments: ["fork-workflows"] as Experiment[],
			chat: { plan_mode: "plan" as const },
		},
		{
			label: "an explore root chat",
			experiments: ["fork-workflows"] as Experiment[],
			chat: { agent_type: "explore" },
		},
		{
			label: "a new chat in plan mode",
			// A new chat has no chat record yet, so the form reports the
			// pending mode through props instead.
			experiments: ["fork-workflows"] as Experiment[],
			isNewChat: true,
			isPlanMode: true,
		},
		{
			label: "a new chat as an explore agent",
			experiments: ["fork-workflows"] as Experiment[],
			isNewChat: true,
			isExploreAgentType: true,
		},
	])(
		"offers no workflows with $label",
		async ({
			experiments,
			chat,
			isNewChat,
			isPlanMode,
			isExploreAgentType,
		}) => {
			const { getUserWorkflows, getOrganizationWorkflows, queryClient } =
				renderComposer({
					chat,
					experiments,
					isNewChat,
					isPlanMode,
					isExploreAgentType,
				});
			// Another page may have filled the shared cache before this chat
			// opened; the gate must hide those workflows too, not skip the fetch.
			queryClient.setQueryData(
				savedWorkflowsKey({
					tier: "user",
					organizationId: MockDefaultOrganization.id,
				}),
				[MockForkSavedWorkflowMetadata],
			);
			await typeIntoComposer("@");
			await screen.findByRole("option", { name: /^@explore/ });
			expect(screen.queryByText("Workflows")).toBeNull();
			expect(getUserWorkflows).not.toHaveBeenCalled();
			expect(getOrganizationWorkflows).not.toHaveBeenCalled();
		},
	);

	it("offers workflows alone when fork-workflows is on and fork-agent-definitions is off", async () => {
		const { getUserWorkflows, getPersonal } = renderComposer({
			experiments: ["fork-workflows"],
			skipAgentDefinitionsExperiment: true,
		});
		getUserWorkflows.mockResolvedValue([MockForkSavedWorkflowMetadata]);
		const user = await typeIntoComposer("@");
		await screen.findByText("Workflows");
		await screen.findByRole("option", {
			name: new RegExp(`^@${MockForkSavedWorkflowMetadata.name}`),
		});
		expect(screen.queryByRole("option", { name: /^@explore/ })).toBeNull();
		expect(getPersonal).not.toHaveBeenCalled();
		await user.keyboard("{Escape}");
	});

	it("keeps Enter from sending while only the workflows still load", async () => {
		const { getUserWorkflows, onEnter } = renderComposer({
			experiments: ["fork-workflows"],
			skipAgentDefinitionsExperiment: true,
		});
		getUserWorkflows.mockReturnValue(new Promise(() => {}));
		const user = await typeIntoComposer("@zzz");
		await screen.findByText("Loading workflows...");
		await user.keyboard("{Enter}");
		expect(onEnter).not.toHaveBeenCalled();
	});

	it("keeps Enter usable when a failed workflow refetch leaves cached data", async () => {
		const { getUserWorkflows, queryClient, onEnter } = renderComposer({
			experiments: ["fork-workflows"],
		});
		// The cached list goes stale at updatedAt 0, so opening the menu
		// refetches it. When that refetch failed, the error used to put the
		// menu in its retry state and swallow Enter even though the cached
		// options were still usable: a failed refetch must leave Enter to
		// pass through to the normal send path.
		queryClient.setQueryData(
			savedWorkflowsKey({
				tier: "user",
				organizationId: MockDefaultOrganization.id,
			}),
			[MockForkSavedWorkflowMetadata],
			{ updatedAt: 0 },
		);
		// The stale refetch must fail while the cached list survives, which
		// the old error state treated as a dead end that swallowed Enter.
		getUserWorkflows.mockRejectedValue(new Error("offline"));
		const userKey = savedWorkflows({
			tier: "user",
			organizationId: MockDefaultOrganization.id,
		}).queryKey;
		const user = await typeIntoComposer("@zzz");
		await waitFor(() =>
			expect(queryClient.getQueryState(userKey)?.status).toBe("error"),
		);
		await user.keyboard("{Enter}");
		expect(onEnter).toHaveBeenCalledTimes(1);
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
