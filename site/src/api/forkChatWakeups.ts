// This file is fork code: the fork-chat-wakeups experiment's API calls live
// here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { ChatWakeup, UpdateChatWakeupRequest } from "./typesGenerated";

/** Fetches the chat's wake-ups, oldest first. */
export const getChatWakeups = async (
	chatId: string,
	signal?: AbortSignal,
): Promise<ChatWakeup[]> => {
	const response = await API.getAxiosInstance().get<ChatWakeup[]>(
		`/api/v2/chats/fork/${chatId}/wakeups`,
		{ signal },
	);
	return response.data;
};

/** Pauses (paused), resumes (active) or cancels (canceled) a wake-up. */
export const updateChatWakeup = async (
	chatId: string,
	wakeupId: string,
	req: UpdateChatWakeupRequest,
): Promise<ChatWakeup> => {
	const response = await API.getAxiosInstance().patch<ChatWakeup>(
		`/api/v2/chats/fork/${chatId}/wakeups/${wakeupId}`,
		req,
	);
	return response.data;
};