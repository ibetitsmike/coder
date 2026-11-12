// This file is fork code: queries for the fork-pinned-skills experiment.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getChatPinnedSkills,
	updateChatPinnedSkills,
} from "#/api/forkChatPinnedSkills";
import type { ChatPinnedSkill } from "#/api/typesGenerated";

export const chatPinnedSkillsKey = (chatId: string) =>
	["chats", "fork-pinned-skills", chatId] as const;

export const chatPinnedSkills = (chatId: string) =>
	queryOptions({
		queryKey: chatPinnedSkillsKey(chatId),
		queryFn: ({ signal }) => getChatPinnedSkills(chatId, signal),
	});

/**
 * Replaces the chat's pinned skills. The response is the stored list, so it
 * goes into the cache at once, before the refetch that every outcome runs.
 */
export const updateChatPinnedSkillsMutation = (
	queryClient: QueryClient,
	chatId: string,
) =>
	mutationOptions({
		mutationFn: (skills: readonly ChatPinnedSkill[]) =>
			updateChatPinnedSkills(chatId, { skills }),
		onSuccess: (pins) => {
			queryClient.setQueryData(chatPinnedSkillsKey(chatId), pins);
		},
		onSettled: () =>
			queryClient.invalidateQueries({
				queryKey: chatPinnedSkillsKey(chatId),
			}),
	});
