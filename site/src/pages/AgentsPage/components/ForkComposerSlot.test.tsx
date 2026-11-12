import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkChatPinnedSkillsApi from "#/api/forkChatPinnedSkills";
import * as forkChatTodosApi from "#/api/forkChatTodos";
import * as forkChatWakeupsApi from "#/api/forkChatWakeups";
import * as forkPluginPackEnablementApi from "#/api/forkPluginPackEnablement";
import { chatTodosKey } from "#/api/queries/forkChatTodos";
import { chatWakeupsKey } from "#/api/queries/forkChatWakeups";
import type {
	ChatPinnedSkill,
	ChatTodoList,
	ChatWakeup,
	Experiment,
} from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
	MockUserOwner,
} from "#/testHelpers/entities";
import { MockChatTodoList } from "#/testHelpers/forkChatTodos";
import {
	MockChatWakeupCron,
	MockChatWakeupInterval,
} from "#/testHelpers/forkChatWakeups";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import {
	MockOrganizationSkillPreferences,
	MockSkills,
} from "#/testHelpers/skills";
import {
	createMockWebSocket,
	type MockWebSocketServer,
} from "#/testHelpers/websockets";
import { OneWayWebSocket } from "#/utils/OneWayWebSocket";
import { ForkComposerSlot } from "./ForkComposerSlot";

vi.mock("#/hooks/useAuthenticated", () => ({
	useAuthenticated: () => ({ user: MockUserOwner }),
}));

const renderSlot = (
	experiments: Experiment[],
	queryClient = createTestQueryClient(),
	isDisabled = false,
) => {
	const { unmount } = renderComponent(
		<QueryClientProvider client={queryClient}>
			<DashboardContext.Provider
				value={{
					entitlements: MockEntitlements,
					experiments,
					appearance: MockAppearanceConfig,
					buildInfo: MockBuildInfo,
					organizations: [MockDefaultOrganization],
					showOrganizations: false,
					canViewOrganizationSettings: false,
				}}
			>
				<ForkComposerSlot
					chatId={MockChat.id}
					isDisabled={isDisabled}
					workspaceSkills={[]}
				/>
			</DashboardContext.Provider>
		</QueryClientProvider>,
	);
	return { queryClient, unmount };
};

const mockTodoWatchSocket = () => {
	let server: MockWebSocketServer | undefined;
	const watch = vi.spyOn(forkChatTodosApi, "watchChatTodos").mockImplementation(
		(chatId) =>
			new OneWayWebSocket<ChatTodoList>({
				apiRoute: `/api/v2/chats/fork/${chatId}/todos/watch`,
				websocketInit: (url, protocol) => {
					const [socket, mockServer] = createMockWebSocket(url, protocol);
					server = mockServer;
					return socket;
				},
			}),
	);
	const publish = (list: ChatTodoList) => {
		if (!server) {
			throw new Error("Todo watch connection was not opened");
		}
		const openServer = server;
		act(() =>
			openServer.publishMessage(
				new MessageEvent("message", { data: JSON.stringify(list) }),
			),
		);
	};
	return { watch, publish };
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkComposerSlot todo list", () => {
	it("does not request todos while fork-chat-todos is off", () => {
		const getTodos = vi
			.spyOn(forkChatTodosApi, "getChatTodos")
			.mockResolvedValue(MockChatTodoList);
		const { watch } = mockTodoWatchSocket();

		renderSlot([]);

		expect(getTodos).not.toHaveBeenCalled();
		expect(watch).not.toHaveBeenCalled();
	});

	it("applies todo lists from the watch socket", async () => {
		vi.spyOn(forkChatTodosApi, "getChatTodos").mockResolvedValue(
			MockChatTodoList,
		);
		const { watch, publish } = mockTodoWatchSocket();

		const { queryClient } = renderSlot(["fork-chat-todos"]);

		await waitFor(() => expect(watch).toHaveBeenCalledWith(MockChat.id));
		const updated: ChatTodoList = {
			version: MockChatTodoList.version + 1,
			todos: MockChatTodoList.todos.map((todo) =>
				todo.id === "build" ? { ...todo, status: "done" } : todo,
			),
		};
		publish(updated);

		await waitFor(() =>
			expect(queryClient.getQueryData(chatTodosKey(MockChat.id))).toEqual(
				updated,
			),
		);
	});

	it("does not watch a list it failed to load", async () => {
		vi.spyOn(forkChatTodosApi, "getChatTodos").mockRejectedValue(
			new Error("forbidden"),
		);
		const { watch } = mockTodoWatchSocket();

		const { queryClient } = renderSlot(["fork-chat-todos"]);

		await waitFor(() =>
			expect(queryClient.getQueryState(chatTodosKey(MockChat.id))?.status).toBe(
				"error",
			),
		);
		expect(watch).not.toHaveBeenCalled();
	});

	it("reads the list again before watching a revisited chat", async () => {
		const getTodos = vi
			.spyOn(forkChatTodosApi, "getChatTodos")
			.mockResolvedValue(MockChatTodoList);
		const { watch } = mockTodoWatchSocket();
		// The app's client keeps unobserved queries, unlike the test default.
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false } },
		});

		const first = renderSlot(["fork-chat-todos"], queryClient);
		await waitFor(() => expect(watch).toHaveBeenCalledTimes(1));
		first.unmount();
		await waitFor(() =>
			expect(
				queryClient
					.getQueryCache()
					.find({ queryKey: chatTodosKey(MockChat.id) }),
			).toBeUndefined(),
		);
		getTodos.mockRejectedValue(new Error("forbidden"));
		renderSlot(["fork-chat-todos"], queryClient);

		await waitFor(() =>
			expect(queryClient.getQueryState(chatTodosKey(MockChat.id))?.status).toBe(
				"error",
			),
		);
		expect(watch).toHaveBeenCalledTimes(1);
	});
});

// Serves wake-ups from a list that a PATCH updates, so the refetch after an
// update returns the new status like the server would.
const mockWakeupsServer = (initial: ChatWakeup[]) => {
	let wakeups = initial;
	vi.spyOn(forkChatWakeupsApi, "getChatWakeups").mockImplementation(
		async () => wakeups,
	);
	return vi
		.spyOn(forkChatWakeupsApi, "updateChatWakeup")
		.mockImplementation(async (_chatId, wakeupId, req) => {
			wakeups = wakeups.map((wakeup) =>
				wakeup.id === wakeupId ? { ...wakeup, status: req.status } : wakeup,
			);
			const updated = wakeups.find((wakeup) => wakeup.id === wakeupId);
			if (!updated) {
				throw new Error(`Unknown wake-up: ${wakeupId}`);
			}
			return updated;
		});
};

describe("ForkComposerSlot wake-ups", () => {
	it("does not request wake-ups while fork-chat-wakeups is off", () => {
		const getWakeups = vi
			.spyOn(forkChatWakeupsApi, "getChatWakeups")
			.mockResolvedValue([]);

		renderSlot([]);

		expect(getWakeups).not.toHaveBeenCalled();
	});

	it("loads todos and wake-ups when both experiments are on", async () => {
		const getTodos = vi
			.spyOn(forkChatTodosApi, "getChatTodos")
			.mockResolvedValue(MockChatTodoList);
		mockTodoWatchSocket();
		const getWakeups = vi
			.spyOn(forkChatWakeupsApi, "getChatWakeups")
			.mockResolvedValue([]);

		renderSlot(["fork-chat-todos", "fork-chat-wakeups"]);

		await waitFor(() => {
			expect(getTodos).toHaveBeenCalledWith(MockChat.id, expect.anything());
			expect(getWakeups).toHaveBeenCalledWith(MockChat.id, expect.anything());
		});
	});

	it("pauses an active wake-up", async () => {
		const updateWakeup = mockWakeupsServer([MockChatWakeupInterval]);
		const user = userEvent.setup();

		const { queryClient } = renderSlot(["fork-chat-wakeups"]);

		await user.click(
			await screen.findByRole("button", { name: "Wake-ups, 1 open" }),
		);
		await user.click(screen.getByRole("button", { name: /^Pause Every 5m: / }));

		await waitFor(() =>
			expect(queryClient.getQueryData(chatWakeupsKey(MockChat.id))).toEqual([
				{ ...MockChatWakeupInterval, status: "paused" },
			]),
		);
		expect(updateWakeup).toHaveBeenCalledWith(
			MockChat.id,
			MockChatWakeupInterval.id,
			{ status: "paused" },
		);
	});

	it("cancels a wake-up", async () => {
		const updateWakeup = mockWakeupsServer([MockChatWakeupInterval]);
		const user = userEvent.setup();

		const { queryClient } = renderSlot(["fork-chat-wakeups"]);

		await user.click(
			await screen.findByRole("button", { name: "Wake-ups, 1 open" }),
		);
		await user.click(
			screen.getByRole("button", { name: /^Cancel Every 5m: / }),
		);

		await waitFor(() =>
			expect(queryClient.getQueryData(chatWakeupsKey(MockChat.id))).toEqual([
				{ ...MockChatWakeupInterval, status: "canceled" },
			]),
		);
		expect(updateWakeup).toHaveBeenCalledWith(
			MockChat.id,
			MockChatWakeupInterval.id,
			{ status: "canceled" },
		);
	});

	it("blocks other updates while one is running", async () => {
		vi.spyOn(forkChatWakeupsApi, "getChatWakeups").mockResolvedValue([
			MockChatWakeupInterval,
			MockChatWakeupCron,
		]);
		const updateWakeup = vi
			.spyOn(forkChatWakeupsApi, "updateChatWakeup")
			.mockReturnValue(new Promise(() => {}));
		const user = userEvent.setup();

		renderSlot(["fork-chat-wakeups"]);

		await user.click(
			await screen.findByRole("button", { name: "Wake-ups, 2 open" }),
		);
		await user.click(screen.getByRole("button", { name: /^Pause Every 5m: / }));

		const cancelCron = screen.getByRole("button", { name: /^Cancel Cron/ });
		await waitFor(() => expect(cancelCron).toBeDisabled());
		await user.click(cancelCron);
		expect(updateWakeup).toHaveBeenCalledTimes(1);
	});

	it("disables wake-up actions while the composer is disabled", async () => {
		const updateWakeup = mockWakeupsServer([MockChatWakeupInterval]);
		const user = userEvent.setup();

		renderSlot(["fork-chat-wakeups"], createTestQueryClient(), true);

		await user.click(
			await screen.findByRole("button", { name: "Wake-ups, 1 open" }),
		);
		await user.click(screen.getByRole("button", { name: /^Pause Every 5m: / }));
		await user.click(
			screen.getByRole("button", { name: /^Cancel Every 5m: / }),
		);
		expect(updateWakeup).not.toHaveBeenCalled();
	});
});

describe("ForkComposerSlot plugin packs", () => {
	it("does not request the chat or its packs while fork-plugin-packs is off", () => {
		const getChat = vi
			.spyOn(API.experimental, "getChat")
			.mockResolvedValue(MockChat);
		const getEnabled = vi
			.spyOn(forkPluginPackEnablementApi, "getEnabledPluginPacks")
			.mockResolvedValue([]);

		renderSlot([]);

		expect(getChat).not.toHaveBeenCalled();
		expect(getEnabled).not.toHaveBeenCalled();
	});

	it("requests the chat's plugin packs while fork-plugin-packs is on", async () => {
		const getChat = vi
			.spyOn(API.experimental, "getChat")
			.mockResolvedValue(MockChat);
		const getEnabled = vi
			.spyOn(forkPluginPackEnablementApi, "getEnabledPluginPacks")
			.mockResolvedValue([]);

		renderSlot(["fork-plugin-packs"]);

		await waitFor(() =>
			expect(getEnabled).toHaveBeenCalledWith({
				kind: "chat",
				chatId: MockChat.id,
			}),
		);
		expect(getChat).toHaveBeenCalledWith(MockChat.id, expect.anything());
	});

	const mockPluginPacksOfChat = (ownerId: string) => {
		vi.spyOn(API.experimental, "getChat").mockResolvedValue({
			...MockChat,
			owner_id: ownerId,
		});
		vi.spyOn(
			forkPluginPackEnablementApi,
			"getEnabledPluginPacks",
		).mockResolvedValue([MockForkPluginPackWithoutPath]);
		vi.spyOn(
			forkPluginPackEnablementApi,
			"getAvailablePluginPacks",
		).mockResolvedValue([MockForkPluginPack]);
		return vi
			.spyOn(forkPluginPackEnablementApi, "setPluginPackEnabled")
			.mockResolvedValue();
	};

	it("lets the chat owner enable a pack", async () => {
		const user = userEvent.setup();
		const setEnabled = mockPluginPacksOfChat(MockUserOwner.id);

		renderSlot(["fork-plugin-packs"]);
		await user.click(
			await screen.findByRole("button", {
				name: "Plugin packs, 1 enabled on this chat",
			}),
		);
		await user.click(
			await screen.findByRole("switch", {
				name: `Enable ${MockForkPluginPack.name}`,
			}),
		);

		await waitFor(() =>
			expect(setEnabled).toHaveBeenCalledWith(
				{ kind: "chat", chatId: MockChat.id },
				MockForkPluginPack.id,
				true,
			),
		);
	});

	it("shows another user's chat packs read-only", async () => {
		const user = userEvent.setup();
		mockPluginPacksOfChat("another-user");

		renderSlot(["fork-plugin-packs"]);
		await user.click(
			await screen.findByRole("button", {
				name: "Plugin packs, 1 enabled on this chat",
			}),
		);

		await screen.findByText(MockForkPluginPack.name);
		expect(screen.queryByRole("switch")).toBeNull();
	});
});

describe("ForkComposerSlot pinned skills", () => {
	const mockDocsPin: ChatPinnedSkill = { source: "personal", name: "docs" };
	const mockReviewerPin: ChatPinnedSkill = {
		source: "personal",
		name: "reviewer",
	};

	const mockPinnedSkillsOfChat = (pins: ChatPinnedSkill[]) => {
		vi.spyOn(API.experimental, "getChat").mockResolvedValue(MockChat);
		vi.spyOn(API.experimental, "getUserSkills").mockResolvedValue(MockSkills);
		vi.spyOn(API.experimental, "getOrganizationSkills").mockResolvedValue([]);
		vi.spyOn(
			API.experimental,
			"getOrganizationSkillPreferences",
		).mockResolvedValue(MockOrganizationSkillPreferences);
		vi.spyOn(forkChatPinnedSkillsApi, "getChatPinnedSkills").mockResolvedValue(
			pins,
		);
		return vi
			.spyOn(forkChatPinnedSkillsApi, "updateChatPinnedSkills")
			.mockImplementation(async (_chatId, req) => [...req.skills]);
	};

	it("does not request pinned skills while fork-pinned-skills is off", () => {
		const getPins = vi
			.spyOn(forkChatPinnedSkillsApi, "getChatPinnedSkills")
			.mockResolvedValue([]);

		renderSlot([]);

		expect(getPins).not.toHaveBeenCalled();
	});

	it("pins a skill from the picker", async () => {
		const user = userEvent.setup();
		const updatePins = mockPinnedSkillsOfChat([mockDocsPin]);

		renderSlot(["fork-pinned-skills"]);
		await user.click(
			await screen.findByRole("button", { name: "Pinned skills, 1 pinned" }),
		);
		await user.click(
			await screen.findByRole("option", { name: "/personal/reviewer" }),
		);

		await waitFor(() => expect(updatePins).toHaveBeenCalledTimes(1));
		expect(updatePins).toHaveBeenCalledWith(MockChat.id, {
			skills: [mockDocsPin, mockReviewerPin],
		});
	});

	it("unpins a skill from its chip", async () => {
		const user = userEvent.setup();
		const updatePins = mockPinnedSkillsOfChat([mockDocsPin, mockReviewerPin]);

		renderSlot(["fork-pinned-skills"]);
		await user.click(
			await screen.findByRole("button", { name: "Pinned skills, 2 pinned" }),
		);
		await user.click(
			await screen.findByRole("button", {
				name: "Remove skill /personal/docs",
			}),
		);

		await waitFor(() => expect(updatePins).toHaveBeenCalledTimes(1));
		expect(updatePins).toHaveBeenCalledWith(MockChat.id, {
			skills: [mockReviewerPin],
		});
	});

	it("disables chip removal while a change is saving", async () => {
		const user = userEvent.setup();
		const updatePins = mockPinnedSkillsOfChat([mockDocsPin, mockReviewerPin]);
		updatePins.mockReturnValue(new Promise(() => {}));

		renderSlot(["fork-pinned-skills"]);
		await user.click(
			await screen.findByRole("button", { name: "Pinned skills, 2 pinned" }),
		);
		await user.click(
			await screen.findByRole("button", {
				name: "Remove skill /personal/docs",
			}),
		);

		const removeReviewer = screen.getByRole("button", {
			name: "Remove skill /personal/reviewer",
		});
		await waitFor(() => expect(removeReviewer).toBeDisabled());
		await user.click(removeReviewer);
		expect(updatePins).toHaveBeenCalledTimes(1);
	});
});
