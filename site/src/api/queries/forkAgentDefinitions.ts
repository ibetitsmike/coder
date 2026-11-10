// This file is fork code: queries for the fork-agent-definitions experiment.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	deleteUserAgentDefinition as deleteUserAgentDefinitionRequest,
	getUserAgentDefinitions,
	updateUserAgentDefinition as updateUserAgentDefinitionRequest,
} from "#/api/forkAgentDefinitions";
import type { UpdateForkAgentDefinitionRequest } from "#/api/typesGenerated";

/** Query key for the current user's agent definitions in an organization. */
export const userAgentDefinitionsKey = (organizationId: string) =>
	["chats", "fork-agent-definitions", "user", organizationId] as const;

/** Query options for the current user's agent definitions in an organization. */
export const userAgentDefinitions = (organizationId: string) =>
	queryOptions({
		queryKey: userAgentDefinitionsKey(organizationId),
		queryFn: () => getUserAgentDefinitions(organizationId),
	});

type UpdateUserAgentDefinitionArgs = {
	organizationId: string;
	name: string;
	req: UpdateForkAgentDefinitionRequest;
};

/**
 * Mutation options that update one of the current user's agent definitions.
 * The organization travels in the variables so a request that settles after
 * the user switches organizations still invalidates its own list.
 */
export const updateUserAgentDefinition = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({
			organizationId,
			name,
			req,
		}: UpdateUserAgentDefinitionArgs) =>
			updateUserAgentDefinitionRequest(organizationId, name, req),
		onSettled: (_data, _error, { organizationId }) =>
			queryClient.invalidateQueries({
				queryKey: userAgentDefinitionsKey(organizationId),
				exact: true,
			}),
	});

type DeleteUserAgentDefinitionArgs = {
	organizationId: string;
	name: string;
};

/**
 * Mutation options that delete one of the current user's agent definitions.
 * The list is invalidated after failures too, because a 404 or a lost
 * response can mean the definition is already gone.
 */
export const deleteUserAgentDefinition = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ organizationId, name }: DeleteUserAgentDefinitionArgs) =>
			deleteUserAgentDefinitionRequest(organizationId, name),
		onSettled: (_data, _error, { organizationId }) =>
			queryClient.invalidateQueries({
				queryKey: userAgentDefinitionsKey(organizationId),
				exact: true,
			}),
	});
