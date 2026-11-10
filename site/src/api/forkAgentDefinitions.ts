// This file is fork code: the fork-agent-definitions experiment's API calls
// live here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { ForkAgentDefinitionMetadata } from "./typesGenerated";

/** Lists a member's personal agent definitions; `user` defaults to the signed-in member. */
export const getUserAgentDefinitions = async (
	organizationId: string,
	user = "me",
): Promise<ForkAgentDefinitionMetadata[]> => {
	const response = await API.getAxiosInstance().get<
		ForkAgentDefinitionMetadata[]
	>(
		`/api/v2/chats/fork/organizations/${organizationId}/members/${user}/agent-definitions`,
	);
	return response.data;
};
