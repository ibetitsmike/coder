import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import { API } from "#/api/api";
import { getChatDescendantQuestions } from "#/api/forkChatQuestions";

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
) =>
	mutationOptions({
		mutationFn: ({ chatId, text }: { chatId: string; text: string }) =>
			API.experimental.createChatMessage(chatId, {
				content: [{ type: "text", text }],
			}),
		onSettled: () =>
			queryClient.invalidateQueries({
				queryKey: chatDescendantQuestionsKey(rootChatId),
			}),
	});
