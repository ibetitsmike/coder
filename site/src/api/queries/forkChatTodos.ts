import { queryOptions } from "react-query";
import { getChatTodos } from "#/api/forkChatTodos";

/** Query key for a chat's todo list; the watch socket writes to it too. */
export const chatTodosKey = (chatId: string) =>
	["chats", "fork-todos", chatId] as const;

/** Query options for a chat's todo list. */
export const chatTodos = (chatId: string) =>
	queryOptions({
		queryKey: chatTodosKey(chatId),
		queryFn: ({ signal }) => getChatTodos(chatId, signal),
		// The todo watch socket keeps this cache current, so refetches add nothing.
		staleTime: Number.POSITIVE_INFINITY,
	});
