import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkChatTodosApi from "#/api/forkChatTodos";
import * as forkChatWakeupsApi from "#/api/forkChatWakeups";
import { chatTodosKey } from "#/api/queries/forkChatTodos";
import type { ChatTodoList, Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
	mockApiError,
} from "#/testHelpers/entities";
import { MockChatTodoList } from "#/testHelpers/forkChatTodos";
import {
	MockChatWakeupCompleted,
	MockChatWakeupInterval,
} from "#/testHelpers/forkChatWakeups";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import {
	createMockWebSocket,
	type MockWebSocketServer,
} from "#/testHelpers/websockets";
import { OneWayWebSocket } from "#/utils/OneWayWebSocket";
import { ForkComposerSlot } from "./ForkComposerSlot";

const renderSlot = (
	experiments: Experiment[],
	queryClient = createTestQueryClient(),
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
				<ForkComposerSlot chatId={MockChat.id} isDisabled={false} />
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

describe("ForkComposerSlot wake-ups", () => {
	it("does not request wake-ups while fork-chat-wakeups is off", () => {
		const getWakeups = vi
			.spyOn(forkChatWakeupsApi, "getChatWakeups")
			.mockResolvedValue([]);

		renderSlot([]);

		expect(getWakeups).not.toHaveBeenCalled();
		expect(screen.queryByRole("button", { name: /Wake-ups/ })).toBeNull();
	});

	it("counts open wake-ups and omits finished ones", async () => {
		vi.spyOn(forkChatWakeupsApi, "getChatWakeups").mockResolvedValue([
			MockChatWakeupInterval,
			MockChatWakeupCompleted,
		]);
		const user = userEvent.setup();

		renderSlot(["fork-chat-wakeups"]);

		await user.click(
			await screen.findByRole("button", { name: "Wake-ups, 1 open" }),
		);
		expect(screen.getByText(MockChatWakeupInterval.prompt)).toBeTruthy();
		expect(screen.queryByText(MockChatWakeupCompleted.prompt)).toBeNull();
	});

	it("pauses an active wake-up", async () => {
		let wakeups = [MockChatWakeupInterval];
		vi.spyOn(forkChatWakeupsApi, "getChatWakeups").mockImplementation(
			async () => wakeups,
		);
		const updateWakeup = vi
			.spyOn(forkChatWakeupsApi, "updateChatWakeup")
			.mockImplementation(async (_chatId, wakeupId, req) => {
				wakeups = wakeups.map((wakeup) =>
					wakeup.id === wakeupId
						? { ...wakeup, status: req.status }
						: wakeup,
				);
				const updated = wakeups.find((wakeup) => wakeup.id === wakeupId);
				if (!updated) {
					throw new Error(`Unknown wake-up: ${wakeupId}`);
				}
				return updated;
			});
		const user = userEvent.setup();

		renderSlot(["fork-chat-wakeups"]);

		await user.click(
			await screen.findByRole("button", { name: "Wake-ups, 1 open" }),
		);
		await user.click(screen.getByRole("button", { name: "Pause" }));

		await waitFor(() =>
			expect(updateWakeup).toHaveBeenCalledWith(
				MockChat.id,
				MockChatWakeupInterval.id,
				{ status: "paused" },
			),
		);
		await screen.findByRole("button", { name: "Resume" });
	});

	it("cancels a wake-up", async () => {
		let wakeups = [MockChatWakeupInterval];
		vi.spyOn(forkChatWakeupsApi, "getChatWakeups").mockImplementation(
			async () => wakeups,
		);
		const updateWakeup = vi
			.spyOn(forkChatWakeupsApi, "updateChatWakeup")
			.mockImplementation(async (_chatId, wakeupId, req) => {
				wakeups = wakeups.map((wakeup) =>
					wakeup.id === wakeupId
						? { ...wakeup, status: req.status }
						: wakeup,
				);
				const updated = wakeups.find((wakeup) => wakeup.id === wakeupId);
				if (!updated) {
					throw new Error(`Unknown wake-up: ${wakeupId}`);
				}
				return updated;
			});
		const user = userEvent.setup();

		renderSlot(["fork-chat-wakeups"]);

		await user.click(
			await screen.findByRole("button", { name: "Wake-ups, 1 open" }),
		);
		await user.click(screen.getByRole("button", { name: "Cancel" }));

		await waitFor(() =>
			expect(updateWakeup).toHaveBeenCalledWith(
				MockChat.id,
				MockChatWakeupInterval.id,
				{ status: "canceled" },
			),
		);
		await waitFor(() =>
			expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull(),
		);
	});

	it("shows the update error under the row", async () => {
		vi.spyOn(forkChatWakeupsApi, "getChatWakeups").mockResolvedValue([
			MockChatWakeupInterval,
		]);
		vi.spyOn(forkChatWakeupsApi, "updateChatWakeup").mockRejectedValue(
			mockApiError({ message: "This wake-up has already finished." }),
		);
		const user = userEvent.setup();

		renderSlot(["fork-chat-wakeups"]);

		await user.click(
			await screen.findByRole("button", { name: "Wake-ups, 1 open" }),
		);
		await user.click(screen.getByRole("button", { name: "Pause" }));

		expect(
			await screen.findByText("This wake-up has already finished."),
		).toBeTruthy();
	});
});
