// This file is fork code: the fork-agent-definitions experiment's API calls
// live here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { ForkAgentDefinitionMetadata } from "./typesGenerated";

const userAgentDefinitionsPath = (organizationId: string, user: string) =>
	`/api/v2/chats/fork/organizations/${organizationId}/members/${user}/agent-definitions`;

/** Lists a user's personal agent definitions in an organization. */
export const getUserAgentDefinitions = async (
	organizationId: string,
	user = "me",
): Promise<ForkAgentDefinitionMetadata[]> => {
	const response = await API.getAxiosInstance().get<
		ForkAgentDefinitionMetadata[]
	>(userAgentDefinitionsPath(organizationId, user));
	return response.data;
};
