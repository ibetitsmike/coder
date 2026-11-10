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
	name: string;
	req: UpdateForkAgentDefinitionRequest;
};

/** Mutation options that update one of the current user's agent definitions. */
export const updateUserAgentDefinition = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: ({ name, req }: UpdateUserAgentDefinitionArgs) =>
			updateUserAgentDefinitionRequest(organizationId, name, req),
		onSettled: () =>
			queryClient.invalidateQueries({
				queryKey: userAgentDefinitionsKey(organizationId),
				exact: true,
			}),
	});

/** Mutation options that delete one of the current user's agent definitions. */
export const deleteUserAgentDefinition = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (name: string) =>
			deleteUserAgentDefinitionRequest(organizationId, name),
		onSuccess: () =>
			queryClient.invalidateQueries({
				queryKey: userAgentDefinitionsKey(organizationId),
				exact: true,
			}),
	});
