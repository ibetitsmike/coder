import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getChatWakeups,
	updateChatWakeup as updateChatWakeupApi,
} from "#/api/forkChatWakeups";
import type { ChatWakeup, ChatWakeupStatus } from "#/api/typesGenerated";
import { forkPanelResyncInterval } from "./forkPanelResync";

export const chatWakeupsRootKey = ["chats", "fork-wakeups"] as const;

/** Query key for a chat's wake-ups. */
export const chatWakeupsKey = (chatId: string) =>
	[...chatWakeupsRootKey, chatId] as const;

/**
 * The states the wake-ups pill shows. The server filters on them, so a chat
 * with a long history of completed wake-ups does not send them all.
 */
const openChatWakeupStatuses: readonly ChatWakeupStatus[] = [
	"active",
	"paused",
];

/** Query options for a chat's active and paused wake-ups, oldest first. */
export const chatWakeups = (chatId: string) =>
	queryOptions({
		queryKey: chatWakeupsKey(chatId),
		queryFn: ({ signal }) =>
			getChatWakeups(chatId, signal, openChatWakeupStatuses),
		refetchInterval: ({ state }) =>
			// An empty poll of an events wake-up moves its next_run_at and
			// publishes nothing, so poll at 30 s while one is active.
			state.data?.some(
				(wakeup) => wakeup.kind === "events" && wakeup.status === "active",
			)
				? 30_000
				: forkPanelResyncInterval,
		// A revisited chat must read again instead of showing cached
		// wake-ups: access may be gone.
		gcTime: 0,
	});

type UpdateChatWakeupVariables = {
	wakeupId: string;
	status: ChatWakeupStatus;
};

/**
 * Pauses, resumes or cancels a wake-up: writes the returned wake-up into the
 * cached list, and refetches after success or failure, since a failure such
 * as a 409 means the cached row is stale.
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
		},
		onSettled: () =>
			queryClient.invalidateQueries({ queryKey: chatWakeupsKey(chatId) }),
	});
