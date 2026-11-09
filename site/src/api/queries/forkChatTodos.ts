import { queryOptions } from "react-query";
import { getChatTodos } from "#/api/forkChatTodos";

export const chatTodosKey = (chatId: string) =>
	["chats", "fork-todos", chatId] as const;

export const chatTodos = (chatId: string) =>
	queryOptions({
		queryKey: chatTodosKey(chatId),
		queryFn: ({ signal }) => getChatTodos(chatId, signal),
		// The todo watch socket keeps this cache current, so refetches add nothing.
		staleTime: Number.POSITIVE_INFINITY,
	});
