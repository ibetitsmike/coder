// This file is fork code: the fork-agent-definitions experiment's API calls
// live here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type {
	ForkAgentDefinition,
	ForkAgentDefinitionMetadata,
	UpdateForkAgentDefinitionRequest,
} from "./typesGenerated";

const userAgentDefinitionsPath = (organizationId: string, user: string) =>
	`/api/v2/chats/fork/organizations/${organizationId}/members/${user}/agent-definitions`;

/** Lists a member's personal agent definitions; `user` defaults to the signed-in member. */
export const getUserAgentDefinitions = async (
	organizationId: string,
	user = "me",
): Promise<ForkAgentDefinitionMetadata[]> => {
	const response = await API.getAxiosInstance().get<
		ForkAgentDefinitionMetadata[]
	>(userAgentDefinitionsPath(organizationId, user));
	return response.data;
};

/** Updates one of a user's personal agent definitions. */
export const updateUserAgentDefinition = async (
	organizationId: string,
	name: string,
	req: UpdateForkAgentDefinitionRequest,
	user = "me",
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().patch<ForkAgentDefinition>(
		`${userAgentDefinitionsPath(organizationId, user)}/${encodeURIComponent(name)}`,
		req,
	);
	return response.data;
};

/** Deletes one of a user's personal agent definitions. */
export const deleteUserAgentDefinition = async (
	organizationId: string,
	name: string,
	user = "me",
): Promise<void> => {
	await API.getAxiosInstance().delete(
		`${userAgentDefinitionsPath(organizationId, user)}/${encodeURIComponent(name)}`,
	);
};
