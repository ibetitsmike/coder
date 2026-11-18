import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as apiModule from "#/api/api";
import { chatWorkflowRunKey } from "#/api/queries/forkWorkflows";
import type { ChatWatchEvent } from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	createMockWebSocket,
	type MockWebSocketServer,
} from "#/testHelpers/websockets";
import { OneWayWebSocket } from "#/utils/OneWayWebSocket";
import { useForkChatWatch } from "./useForkChatWatch";

const renderWatch = (enabled: boolean) => {
	const servers: MockWebSocketServer[] = [];
	vi.spyOn(apiModule, "watchChats").mockImplementation(
		() =>
			new OneWayWebSocket<ChatWatchEvent>({
				apiRoute: "/api/v2/chats/watch",
				websocketInit: (url, protocol) => {
					const [socket, server] = createMockWebSocket(url, protocol);
					servers.push(server);
					return socket;
				},
			}),
	);
	const queryClient = new QueryClient();
	const hook = renderHook(() => useForkChatWatch(enabled), {
		wrapper: ({ children }: React.PropsWithChildren) => (
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		),
	});
	return { queryClient, servers, ...hook };
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("useForkChatWatch", () => {
	it("refetches the run a fork_change event names", () => {
		const { queryClient, servers } = renderWatch(true);
		const runKey = chatWorkflowRunKey(MockChat.id, "run-1");
		queryClient.setQueryData(runKey, {});

		servers[0].publishMessage(
			new MessageEvent("message", {
				data: JSON.stringify({
					kind: "fork_change",
					chat: {},
					fork_change: {
						chat_id: MockChat.id,
						root_chat_id: MockChat.id,
						facets: ["workflow_logs"],
						workflow_run_id: "run-1",
					},
				}),
			}),
		);

		expect(queryClient.getQueryState(runKey)?.isInvalidated).toBe(true);
	});

	it("refetches the fork panels when the socket opens", () => {
		const { queryClient, servers } = renderWatch(true);
		const runKey = chatWorkflowRunKey(MockChat.id, "run-1");
		queryClient.setQueryData(runKey, {});

		servers[0].publishOpen(new Event("open"));

		expect(queryClient.getQueryState(runKey)?.isInvalidated).toBe(true);
	});

	it("closes the socket on unmount", () => {
		const { servers, unmount } = renderWatch(true);
		unmount();
		expect(servers[0].isConnectionOpen).toBe(false);
	});

	it("opens no socket while disabled", () => {
		const { servers } = renderWatch(false);
		expect(servers).toHaveLength(0);
	});
});
