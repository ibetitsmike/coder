import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import { API } from "#/api/api";
import {
	answerChatSecretQuestion,
	getChatDescendantQuestions,
} from "#/api/forkChatQuestions";
import type { ChatDescendantQuestion } from "#/api/typesGenerated";
import {
	invalidateChatDebugRuns,
	invalidateChatEntity,
	invalidateChatPrompts,
} from "./chats";

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

// The root page does not watch the descendant's stream, so it refreshes the
// descendant's caches as createChatMessage does.
const invalidateDescendantChat = (queryClient: QueryClient, chatId: string) => {
	void invalidateChatDebugRuns(queryClient, chatId);
	void invalidateChatEntity(queryClient, chatId);
	void invalidateChatPrompts(queryClient, chatId);
};

/**
 * Sends an answer to the descendant chat that asked the question, then
 * reloads the root chat's pending questions.
 */
export const answerChatDescendantQuestion = (
	queryClient: QueryClient,
	rootChatId: string,
) =>
	mutationOptions({
		mutationFn: async ({
			question,
			text,
		}: {
			question: ChatDescendantQuestion;
			text: string;
		}) => {
			// Any user message answers the chat's pending question, so an
			// answer to a question that another tab already answered would
			// retire the chat's next question.
			const pending = await getChatDescendantQuestions(rootChatId);
			const stillPending = pending.some(
				(item) =>
					item.chat_id === question.chat_id &&
					item.tool_call_id === question.tool_call_id &&
					item.created_at === question.created_at,
			);
			if (!stillPending) {
				throw new Error(
					"This question was already answered. The next one loads shortly.",
				);
			}
			return API.experimental.createChatMessage(question.chat_id, {
				content: [{ type: "text", text }],
			});
		},
		onSuccess: (_, { question }) =>
			invalidateDescendantChat(queryClient, question.chat_id),
		onSettled: () =>
			queryClient.invalidateQueries({
				queryKey: chatDescendantQuestionsKey(rootChatId),
			}),
	});

/**
 * Sends the value of a secret question to the descendant chat that asked it,
 * then reloads the root chat's pending questions.
 */
export const answerChatDescendantSecretQuestion = (
	queryClient: QueryClient,
	rootChatId: string,
) =>
	mutationOptions({
		mutationFn: ({
			question,
			value,
		}: {
			question: ChatDescendantQuestion;
			value: string;
		}) =>
			answerChatSecretQuestion(question.chat_id, question.tool_call_id, {
				value,
			}),
		onSuccess: (_, { question }) =>
			invalidateDescendantChat(queryClient, question.chat_id),
		onSettled: () =>
			queryClient.invalidateQueries({
				queryKey: chatDescendantQuestionsKey(rootChatId),
			}),
	});
