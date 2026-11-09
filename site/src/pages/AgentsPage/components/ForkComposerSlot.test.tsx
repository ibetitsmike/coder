import { act, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkChatTodosApi from "#/api/forkChatTodos";
import type { ChatTodoList, Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockChatTodoList } from "#/testHelpers/forkChatTodos";
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

const renderSlot = (experiments: Experiment[]) => {
	renderComponent(
		<QueryClientProvider client={createTestQueryClient()}>
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

		renderSlot(["fork-chat-todos"]);

		await screen.findByRole("button", { name: "Todo list, 1/3 done" });
		await waitFor(() => expect(watch).toHaveBeenCalledWith(MockChat.id));
		publish({
			version: MockChatTodoList.version + 1,
			todos: MockChatTodoList.todos.map((todo) =>
				todo.id === "build" ? { ...todo, status: "done" } : todo,
			),
		});

		await screen.findByRole("button", { name: "Todo list, 2/3 done" });
	});

	it("does not watch a list it failed to load", async () => {
		vi.spyOn(forkChatTodosApi, "getChatTodos").mockRejectedValue(
			new Error("forbidden"),
		);
		const { watch } = mockTodoWatchSocket();

		renderSlot(["fork-chat-todos"]);

		await screen.findByRole("button", { name: "Todo list failed to load" });
		expect(watch).not.toHaveBeenCalled();
	});
});
