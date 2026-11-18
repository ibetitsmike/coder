// This file is fork code: the embedded agent page renders outside
// AgentsPageLayout, whose chat watch socket carries the fork panel events,
// so it opens its own socket for them.

import { useEffect } from "react";
import { useQueryClient } from "react-query";
import { watchChats } from "#/api/api";
import { createForkChatWatch } from "#/api/queries/forkChatWatch";
import { createReconnectingWebSocket } from "#/utils/reconnectingWebSocket";

/** Refetches the fork panels from the chat watch stream while enabled. */
export const useForkChatWatch = (enabled: boolean) => {
	const queryClient = useQueryClient();
	useEffect(() => {
		if (!enabled) {
			return;
		}
		const forkWatch = createForkChatWatch(queryClient);
		const disposeSocket = createReconnectingWebSocket({
			connect() {
				const ws = watchChats();
				ws.addEventListener("message", (event) => {
					if (!event.parseError) {
						forkWatch.onEvent(event.parsedMessage);
					}
				});
				return ws;
			},
			onOpen: forkWatch.onOpen,
		});
		return () => {
			disposeSocket();
			forkWatch.dispose();
		};
	}, [enabled, queryClient]);
};
