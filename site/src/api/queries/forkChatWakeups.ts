import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getChatWakeups,
	updateChatWakeup as updateChatWakeupApi,
} from "#/api/forkChatWakeups";
import type { ChatWakeup } from "#/api/typesGenerated";

/** Query key for a chat's wake-ups. */
export const chatWakeupsKey = (chatId: string) =>
	["chats", "fork-wakeups", chatId] as const;

/** Query options for a chat's wake-ups, oldest first. */
export const chatWakeups = (chatId: string) =>
	queryOptions({
		queryKey: chatWakeupsKey(chatId),
		queryFn: ({ signal }) => getChatWakeups(chatId, signal),
		// Wake-ups have no watch socket, so poll to notice worker changes.
		refetchInterval: 30_000,
	});

type UpdateChatWakeupVariables = {
	wakeupId: string;
	status: ChatWakeup["status"];
};

/** Mutation options to pause, resume or cancel one of the chat's wake-ups. */
export const updateChatWakeup = (queryClient: QueryClient, chatId: string) =>
	mutationOptions({
		mutationFn: ({ wakeupId, status }: UpdateChatWakeupVariables) =>
			updateChatWakeupApi(chatId, wakeupId, { status }),
		onSuccess: (wakeup) => {
			// Write the returned wake-up so the row flips without waiting for
			// the refetch, then invalidate to pull in worker-made changes.
			queryClient.setQueryData<ChatWakeup[]>(chatWakeupsKey(chatId), (old) =>
				old?.map((oldWakeup) =>
					oldWakeup.id === wakeup.id ? wakeup : oldWakeup,
				),
			);
			void queryClient.invalidateQueries({ queryKey: chatWakeupsKey(chatId) });
		},
	});