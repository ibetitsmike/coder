// This file is fork code: the fork-nested-subagents experiment's descendant
// question calls live here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { ChatDescendantQuestion } from "./typesGenerated";

/** Fetches the unanswered ask_user_question calls of a chat's family, oldest first. */
export const getChatDescendantQuestions = async (
	chatId: string,
	signal?: AbortSignal,
): Promise<ChatDescendantQuestion[]> => {
	const response = await API.getAxiosInstance().get<ChatDescendantQuestion[]>(
		`/api/v2/chats/fork/${chatId}/questions`,
		{ signal },
	);
	return response.data;
};
