// This file is fork code: the fork-pinned-skills experiment's API calls.

import { API } from "./api";
import type {
	ChatPinnedSkill,
	UpdateChatPinnedSkillsRequest,
} from "./typesGenerated";

/** Lists the skills pinned to the chat. */
export const getChatPinnedSkills = async (
	chatId: string,
): Promise<ChatPinnedSkill[]> => {
	const response = await API.getAxiosInstance().get<ChatPinnedSkill[]>(
		`/api/v2/chats/fork/${chatId}/pinned-skills`,
	);
	return response.data;
};

/** Replaces every pinned skill of the chat and returns the new list. */
export const updateChatPinnedSkills = async (
	chatId: string,
	req: UpdateChatPinnedSkillsRequest,
): Promise<ChatPinnedSkill[]> => {
	const response = await API.getAxiosInstance().put<ChatPinnedSkill[]>(
		`/api/v2/chats/fork/${chatId}/pinned-skills`,
		req,
	);
	return response.data;
};
