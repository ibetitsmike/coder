// This file is fork code: queries for the fork-agent-definitions experiment.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	createUserAgentDefinition as createUserAgentDefinitionRequest,
	deleteUserAgentDefinition as deleteUserAgentDefinitionRequest,
	getUserAgentDefinition,
	getUserAgentDefinitions,
	updateUserAgentDefinition as updateUserAgentDefinitionRequest,
} from "#/api/forkAgentDefinitions";
import type {
	CreateForkAgentDefinitionRequest,
	UpdateForkAgentDefinitionRequest,
} from "#/api/typesGenerated";

/** Query key for the current user's agent definitions in an organization. */
export const userAgentDefinitionsKey = (organizationId: string) =>
	["chats", "fork-agent-definitions", "user", organizationId] as const;

/** Query options for the current user's agent definitions in an organization. */
export const userAgentDefinitions = (organizationId: string) =>
	queryOptions({
		queryKey: userAgentDefinitionsKey(organizationId),
		queryFn: () => getUserAgentDefinitions(organizationId),
	});

/**
 * One definition with its markdown content. The key extends the list key, so
 * invalidating the list refreshes an open editor's copy too.
 */
export const userAgentDefinition = (organizationId: string, name: string) =>
	queryOptions({
		queryKey: [...userAgentDefinitionsKey(organizationId), name] as const,
		queryFn: () => getUserAgentDefinition(organizationId, name),
	});

type CreateUserAgentDefinitionArgs = {
	organizationId: string;
	req: CreateForkAgentDefinitionRequest;
};

/**
 * The list is invalidated after failures too, because a lost response can
 * hide a created definition.
 */
export const createUserAgentDefinition = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ organizationId, req }: CreateUserAgentDefinitionArgs) =>
			createUserAgentDefinitionRequest(organizationId, req),
		onSuccess: ({ content: _content, ...created }, { organizationId }) => {
			queryClient.setQueryData(
				userAgentDefinitions(organizationId).queryKey,
				// A refetch can list the definition before this response
				// arrives, so replace that row instead of adding a second one.
				(definitions) =>
					definitions && [
						...definitions.filter(
							(listed) =>
								listed.id !== created.id && listed.name !== created.name,
						),
						created,
					],
			);
		},
		onSettled: (_data, _error, { organizationId }) =>
			queryClient.invalidateQueries({
				queryKey: userAgentDefinitionsKey(organizationId),
				exact: true,
			}),
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
		// Writing the result keeps an accepted change visible, in the list and
		// in a reopened editor, even if the reconciling refetch below fails.
		onSuccess: (definition, { organizationId, name }) => {
			const { content: _content, ...updated } = definition;
			queryClient.setQueryData(
				userAgentDefinitions(organizationId).queryKey,
				(definitions) =>
					definitions?.map((listed) =>
						listed.id === updated.id ? updated : listed,
					),
			);
			queryClient.setQueryData(
				userAgentDefinition(organizationId, name).queryKey,
				definition,
			);
		},
		// Not exact, so a reopened editor refetches the definition it saved.
		onSettled: (_data, _error, { organizationId }) =>
			queryClient.invalidateQueries({
				queryKey: userAgentDefinitionsKey(organizationId),
			}),
	});

type DeleteUserAgentDefinitionArgs = {
	organizationId: string;
	name: string;
};

/**
 * Mutation options that delete one of the current user's agent definitions.
 * The list is invalidated after failures too, because a 404 or a lost
 * response can mean the definition is already gone. A success also removes
 * the row from the cache so a failed refetch cannot leave it actionable.
 */
export const deleteUserAgentDefinition = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ organizationId, name }: DeleteUserAgentDefinitionArgs) =>
			deleteUserAgentDefinitionRequest(organizationId, name),
		onSuccess: (_data, { organizationId, name }) => {
			queryClient.setQueryData(
				userAgentDefinitions(organizationId).queryKey,
				(definitions) =>
					definitions?.filter((definition) => definition.name !== name),
			);
		},
		onSettled: (_data, _error, { organizationId }) =>
			queryClient.invalidateQueries({
				queryKey: userAgentDefinitionsKey(organizationId),
				exact: true,
			}),
	});
