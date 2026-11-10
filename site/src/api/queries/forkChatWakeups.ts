import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getChatWakeups,
	updateChatWakeup as updateChatWakeupApi,
} from "#/api/forkChatWakeups";
import type { ChatWakeup, ChatWakeupStatus } from "#/api/typesGenerated";

/** Query key for a chat's wake-ups. */
export const chatWakeupsKey = (chatId: string) =>
	["chats", "fork-wakeups", chatId] as const;

/** Query options for a chat's wake-ups, oldest first. */
export const chatWakeups = (chatId: string) =>
	queryOptions({
		queryKey: chatWakeupsKey(chatId),
		queryFn: ({ signal }) => getChatWakeups(chatId, signal),
		// Wake-ups have no watch socket, so poll to see worker-made changes.
		refetchInterval: 30_000,
	});

type UpdateChatWakeupVariables = {
	wakeupId: string;
	status: ChatWakeupStatus;
};

/**
 * Pauses, resumes or cancels a wake-up: writes the returned wake-up into the
 * cached list and refetches to pick up worker-made changes.
 */
export const updateChatWakeup = (queryClient: QueryClient, chatId: string) =>
	mutationOptions({
		mutationFn: ({ wakeupId, status }: UpdateChatWakeupVariables) =>
			updateChatWakeupApi(chatId, wakeupId, { status }),
		onSuccess: (wakeup) => {
			queryClient.setQueryData<ChatWakeup[]>(chatWakeupsKey(chatId), (old) =>
				old?.map((oldWakeup) =>
					oldWakeup.id === wakeup.id ? wakeup : oldWakeup,
				),
			);
			void queryClient.invalidateQueries({ queryKey: chatWakeupsKey(chatId) });
		},
	});
