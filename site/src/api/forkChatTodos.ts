// This file is fork code: the fork-chat-todos experiment's API calls live
// here so api.ts, an upstream file, stays unchanged.

import { OneWayWebSocket } from "#/utils/OneWayWebSocket";
import { API, SessionTokenCookie } from "./api";
import type { ChatTodoList } from "./typesGenerated";

export const getChatTodos = async (
	chatId: string,
	signal?: AbortSignal,
): Promise<ChatTodoList> => {
	const response = await API.getAxiosInstance().get<ChatTodoList>(
		`/api/v2/chats/fork/${chatId}/todos`,
		{ signal },
	);
	return response.data;
};

/**
 * Streams the chat's todo list: the server sends the current list on connect
 * and the whole list again after every write.
 */
export const watchChatTodos = (
	chatId: string,
): OneWayWebSocket<ChatTodoList> => {
	const searchParams: Record<string, string> = {};
	const token = API.getSessionToken();
	if (token) {
		searchParams[SessionTokenCookie] = token;
	}
	return new OneWayWebSocket({
		apiRoute: `/api/v2/chats/fork/${chatId}/todos/watch`,
		searchParams,
	});
};
