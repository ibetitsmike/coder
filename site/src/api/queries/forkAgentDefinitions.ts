// This file is fork code: queries for the fork-agent-definitions experiment.

import { isAxiosError } from "axios";
import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	createOrganizationAgentDefinition,
	createUserAgentDefinition,
	deleteOrganizationAgentDefinition,
	deleteUserAgentDefinition,
	getOrganizationAgentDefinition,
	getOrganizationAgentDefinitions,
	getUserAgentDefinition,
	getUserAgentDefinitions,
	updateOrganizationAgentDefinition,
	updateUserAgentDefinition,
} from "#/api/forkAgentDefinitions";
import { compareUpdatedAtInstants } from "#/api/queries/chats";
import type {
	CreateForkAgentDefinitionRequest,
	UpdateForkAgentDefinitionRequest,
} from "#/api/typesGenerated";

/**
 * Where agent definitions live: the current user's personal definitions in
 * an organization, or the organization's own.
 */
export type AgentDefinitionScope = {
	tier: "user" | "organization";
	organizationId: string;
};

/** Whether two scopes name the same definitions. */
export const isSameScope = (a: AgentDefinitionScope, b: AgentDefinitionScope) =>
	a.tier === b.tier && a.organizationId === b.organizationId;

/** Query key for the agent definitions in a scope. */
export const agentDefinitionsKey = ({
	tier,
	organizationId,
}: AgentDefinitionScope) =>
	["chats", "fork-agent-definitions", tier, organizationId] as const;

/** Query options for the agent definitions in a scope. */
export const agentDefinitions = (scope: AgentDefinitionScope) =>
	queryOptions({
		queryKey: agentDefinitionsKey(scope),
		queryFn: () =>
			scope.tier === "user"
				? getUserAgentDefinitions(scope.organizationId)
				: getOrganizationAgentDefinitions(scope.organizationId),
	});

/**
 * The key extends the list key, so invalidating the list refetches this too.
 * A 404 drops the requested row from the cached list unless a refetch has
 * replaced that row since.
 */
export const agentDefinition = (
	queryClient: QueryClient,
	scope: AgentDefinitionScope,
	name: string,
) =>
	queryOptions({
		queryKey: [...agentDefinitionsKey(scope), name] as const,
		queryFn: async () => {
			const listKey = agentDefinitions(scope).queryKey;
			const listed = queryClient
				.getQueryData(listKey)
				?.find((definition) => definition.name === name);
			try {
				return await (scope.tier === "user"
					? getUserAgentDefinition(scope.organizationId, name)
					: getOrganizationAgentDefinition(scope.organizationId, name));
			} catch (error) {
				if (isAxiosError(error) && error.response?.status === 404 && listed) {
					queryClient.setQueryData(listKey, (definitions) =>
						definitions?.filter((definition) => definition !== listed),
					);
				}
				throw error;
			}
		},
	});

type Timestamped = { updated_at: string };

/** Whether a was updated after b, to the timestamps' full precision. */
export const isNewer = (a: Timestamped, b: Timestamped) =>
	compareUpdatedAtInstants(a.updated_at, b.updated_at) > 0;

/** Whether a request failed with no response, so it may still have applied. */
export const isResponseLost = (error: unknown) =>
	isAxiosError(error) && error.response === undefined;

// Callers reconcile an unknown outcome against the refetched list, so only
// then does the mutation stay pending until the refetch settles.
const invalidateList = (
	queryClient: QueryClient,
	scope: AgentDefinitionScope,
	{ exact, awaitRefetch }: { exact: boolean; awaitRefetch: boolean },
) => {
	const refetch = queryClient.invalidateQueries({
		queryKey: agentDefinitionsKey(scope),
		exact,
	});
	return awaitRefetch ? refetch : undefined;
};

type CreateAgentDefinitionArgs = {
	scope: AgentDefinitionScope;
	req: CreateForkAgentDefinitionRequest;
};

/**
 * The list is invalidated after failures too, because a lost response can
 * hide a created definition.
 */
export const createAgentDefinition = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ scope, req }: CreateAgentDefinitionArgs) =>
			scope.tier === "user"
				? createUserAgentDefinition(scope.organizationId, req)
				: createOrganizationAgentDefinition(scope.organizationId, req),
		// A list without the name may predate the commit or follow a delete;
		// only the refetch below can tell, so the created row is added until it
		// settles. The rows keep the API's order by name.
		onSuccess: ({ content: _content, ...created }, { scope }) => {
			queryClient.setQueryData(
				agentDefinitions(scope).queryKey,
				(definitions) => {
					// A refetch can list the definition before this response
					// arrives, or still list a deleted one with this name. Its
					// row replaces that one unless the row is newer, such as a
					// later change or a recreate.
					const listed = definitions?.find(
						(definition) =>
							definition.id === created.id || definition.name === created.name,
					);
					if (!definitions || (listed && isNewer(listed, created))) {
						return definitions;
					}
					return [
						...definitions.filter((definition) => definition !== listed),
						created,
					].sort((a, b) => (a.name < b.name ? -1 : 1));
				},
			);
		},
		onSettled: (_data, error, { scope }) =>
			invalidateList(queryClient, scope, {
				exact: true,
				awaitRefetch: isResponseLost(error),
			}),
	});

type UpdateAgentDefinitionArgs = {
	scope: AgentDefinitionScope;
	name: string;
	req: UpdateForkAgentDefinitionRequest;
};

/**
 * Mutation options that update an agent definition. The scope travels in the
 * variables so a request that settles after the user switches organizations
 * still invalidates its own list.
 */
export const updateAgentDefinition = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ scope, name, req }: UpdateAgentDefinitionArgs) =>
			scope.tier === "user"
				? updateUserAgentDefinition(scope.organizationId, name, req)
				: updateOrganizationAgentDefinition(scope.organizationId, name, req),
		// Writing the result keeps an accepted change visible even if the
		// reconciling refetch below fails.
		// Rows match by name, as the request does, so a stale row of a
		// recreated definition takes the response. A response older than a
		// cached change does not replace it.
		onSuccess: (definition, { scope, name }) => {
			const { content: _content, ...updated } = definition;
			queryClient.setQueryData(
				agentDefinitions(scope).queryKey,
				(definitions) =>
					definitions?.map((listed) =>
						listed.name === updated.name && !isNewer(listed, updated)
							? updated
							: listed,
					),
			);
			queryClient.setQueryData(
				agentDefinition(queryClient, scope, name).queryKey,
				(cached) =>
					cached && isNewer(cached, definition) ? cached : definition,
			);
		},
		// Not exact, so a reopened editor refetches the definition it saved.
		onSettled: (_data, error, { scope }) =>
			invalidateList(queryClient, scope, {
				exact: false,
				awaitRefetch: isResponseLost(error),
			}),
	});

type DeleteAgentDefinitionArgs = {
	scope: AgentDefinitionScope;
	name: string;
};

/**
 * Mutation options that delete an agent definition. The list is invalidated after failures too, because a 404 or a lost
 * response can mean the definition is already gone. A success also removes
 * the row from the cache so a failed refetch cannot leave it actionable.
 */
export const deleteAgentDefinition = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ scope, name }: DeleteAgentDefinitionArgs) =>
			scope.tier === "user"
				? deleteUserAgentDefinition(scope.organizationId, name)
				: deleteOrganizationAgentDefinition(scope.organizationId, name),
		onSuccess: (_data, { scope, name }) => {
			queryClient.setQueryData(
				agentDefinitions(scope).queryKey,
				(definitions) =>
					definitions?.filter((definition) => definition.name !== name),
			);
		},
		onSettled: (_data, error, { scope }) =>
			invalidateList(queryClient, scope, {
				exact: true,
				awaitRefetch:
					isResponseLost(error) ||
					(isAxiosError(error) && error.response?.status === 404),
			}),
	});
