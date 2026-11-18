import { waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as apiModule from "#/api/api";
import { chatWorkflowRunKey } from "#/api/queries/forkWorkflows";
import type { ChatWatchEvent } from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import { renderWithAuth } from "#/testHelpers/renderHelpers";
import {
	createMockWebSocket,
	type MockWebSocketServer,
} from "#/testHelpers/websockets";
import { OneWayWebSocket } from "#/utils/OneWayWebSocket";
import AgentEmbedPage from "./AgentEmbedPage";

afterEach(() => {
	vi.restoreAllMocks();
});

describe("AgentEmbedPage", () => {
	it("refetches the fork panels a fork_change event names", async () => {
		let chatWatchServer: MockWebSocketServer | undefined;
		vi.spyOn(apiModule, "watchChats").mockImplementation(
			() =>
				new OneWayWebSocket<ChatWatchEvent>({
					apiRoute: "/api/v2/chats/watch",
					websocketInit: (url, protocol) => {
						const [socket, server] = createMockWebSocket(url, protocol);
						chatWatchServer = server;
						return socket;
					},
				}),
		);
		const { queryClient } = renderWithAuth(<AgentEmbedPage />, {
			path: "/agents/:agentId/embed",
			route: `/agents/${MockChat.id}/embed`,
		});
		await waitFor(() => expect(chatWatchServer).toBeDefined());
		const runKey = chatWorkflowRunKey(MockChat.id, "run-1");
		queryClient.setQueryData(runKey, {});

		chatWatchServer?.publishMessage(
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
});
