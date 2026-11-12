// This file is fork code: the fork-pinned-skills experiment's API calls.

import { API } from "./api";
import type {
	ChatPinnedSkill,
	UpdateChatPinnedSkillsRequest,
} from "./typesGenerated";

export const getChatPinnedSkills = async (
	chatId: string,
	signal?: AbortSignal,
): Promise<ChatPinnedSkill[]> => {
	const response = await API.getAxiosInstance().get<ChatPinnedSkill[]>(
		`/api/v2/chats/fork/${chatId}/pinned-skills`,
		{ signal },
	);
	return response.data;
};

/** Replaces every pinned skill of the chat. */
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
