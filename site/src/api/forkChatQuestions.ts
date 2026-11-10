// This file is fork code: the fork-nested-subagents experiment's descendant
// question calls live here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type {
	ChatDescendantQuestion,
	ChatSecretAnswerRequest,
} from "./typesGenerated";

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

/**
 * Answers the pending secret question toolCallId of chatId. The server keeps
 * the value out of the chat's messages.
 */
export const answerChatSecretQuestion = async (
	chatId: string,
	toolCallId: string,
	req: ChatSecretAnswerRequest,
): Promise<void> => {
	await API.getAxiosInstance().post(
		`/api/v2/chats/fork/${chatId}/questions/${encodeURIComponent(toolCallId)}/secret-answer`,
		req,
	);
};
