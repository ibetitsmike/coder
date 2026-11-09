import { queryOptions } from "react-query";
import { getChatTodos } from "#/api/forkChatTodos";

export const chatTodosKey = (chatId: string) =>
	["chats", "fork-todos", chatId] as const;

export const chatTodos = (chatId: string) =>
	queryOptions({
		queryKey: chatTodosKey(chatId),
		queryFn: ({ signal }) => getChatTodos(chatId, signal),
		// The todo watch socket writes every change into this cache and resends
		// the current list when it reconnects, so a refetch never adds anything.
		staleTime: Number.POSITIVE_INFINITY,
	});
