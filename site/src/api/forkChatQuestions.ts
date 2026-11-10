// This file is fork code: the fork-nested-subagents experiment's descendant
// question calls and their query definitions live here so api.ts and
// api/queries, upstream files, stay unchanged.

import { type QueryClient, queryOptions } from "react-query";
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

/** Query key for the pending descendant questions of a root chat's family. */
export const chatDescendantQuestionsKey = (rootChatId: string) =>
	["chats", "fork-descendant-questions", rootChatId] as const;

/** Query options for the pending descendant questions of a root chat's family. */
export const chatDescendantQuestions = (rootChatId: string) =>
	queryOptions({
		queryKey: chatDescendantQuestionsKey(rootChatId),
		queryFn: ({ signal }) => getChatDescendantQuestions(rootChatId, signal),
		// A descendant's question raises no event on the root chat's stream.
		refetchInterval: 5_000,
	});

/**
 * Sends an answer to the descendant chat that asked the question, then
 * reloads the root chat's pending questions.
 */
export const answerChatDescendantQuestion = (
	queryClient: QueryClient,
	rootChatId: string,
) => ({
	mutationFn: ({ chatId, text }: { chatId: string; text: string }) =>
		API.experimental.createChatMessage(chatId, {
			content: [{ type: "text", text }],
		}),
	onSettled: () =>
		queryClient.invalidateQueries({
			queryKey: chatDescendantQuestionsKey(rootChatId),
		}),
});
