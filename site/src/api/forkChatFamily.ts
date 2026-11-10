// This file is fork code: the fork-nested-subagents experiment's family API
// calls live here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { ForkChatFamilyCapacity } from "./typesGenerated";

/** Fetches the subagent capacity of the family under a root chat. */
export const getForkChatFamilyCapacity = async (
	rootChatId: string,
	signal?: AbortSignal,
): Promise<ForkChatFamilyCapacity> => {
	const response = await API.getAxiosInstance().get<ForkChatFamilyCapacity>(
		`/api/v2/chats/fork/${rootChatId}/family`,
		{ signal },
	);
	return response.data;
};
