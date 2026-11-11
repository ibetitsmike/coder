// This file is fork code: queries for the fork-agent-definitions experiment.

import { isAxiosError } from "axios";
import {
	hashKey,
	mutationOptions,
	type QueryClient,
	type QueryKey,
	queryOptions,
} from "react-query";
import {
	type AgentTypesParams,
	createOrganizationAgentDefinition,
	createProjectAgentDefinition,
	createUserAgentDefinition,
	deleteOrganizationAgentDefinition,
	deleteProjectAgentDefinition,
	deleteUserAgentDefinition,
	getAgentTypes,
	getOrganizationAgentDefinition,
	getOrganizationAgentDefinitionACL,
	getOrganizationAgentDefinitionACLAvailable,
	getOrganizationAgentDefinitions,
	getProjectAgentDefinition,
	getProjectAgentDefinitions,
	getUserAgentDefinition,
	getUserAgentDefinitions,
	updateOrganizationAgentDefinition,
	updateOrganizationAgentDefinitionACL,
	updateProjectAgentDefinition,
	updateUserAgentDefinition,
} from "#/api/forkAgentDefinitions";
import { compareUpdatedAtInstants } from "#/api/queries/chats";
import type {
	ACLAvailable,
	CreateForkAgentDefinitionRequest,
	ForkAgentDefinition,
	ForkAgentDefinitionMetadata,
	UpdateForkAgentDefinitionRequest,
	UpdateOrganizationSkillACLRequest,
	UsersRequest,
} from "#/api/typesGenerated";

/**
 * Where agent definitions live: the current user's personal definitions in
 * an organization, the organization's own, or a project's.
 */
export type AgentDefinitionScope =
	| { tier: "user" | "organization"; organizationId: string }
	| { tier: "project"; organizationId: string; projectId: string };

/** Query key for the agent definitions in a scope. */
export const agentDefinitionsKey = (scope: AgentDefinitionScope) =>
	[
		"chats",
		"fork-agent-definitions",
		scope.tier,
		scope.organizationId,
		...(scope.tier === "project" ? [scope.projectId] : []),
	] as const;

/** Whether two scopes name the same definitions. */
export const isSameScope = (a: AgentDefinitionScope, b: AgentDefinitionScope) =>
	hashKey(agentDefinitionsKey(a)) === hashKey(agentDefinitionsKey(b));

type ScopeEndpoints = {
	list: () => Promise<ForkAgentDefinitionMetadata[]>;
	get: (name: string) => Promise<ForkAgentDefinition>;
	create: (
		req: CreateForkAgentDefinitionRequest,
	) => Promise<ForkAgentDefinition>;
	update: (
		name: string,
		req: UpdateForkAgentDefinitionRequest,
	) => Promise<ForkAgentDefinition>;
	remove: (name: string) => Promise<void>;
};

const scopeEndpoints = (scope: AgentDefinitionScope): ScopeEndpoints => {
	const { organizationId } = scope;
	switch (scope.tier) {
		case "user":
			return {
				list: () => getUserAgentDefinitions(organizationId),
				get: (name) => getUserAgentDefinition(organizationId, name),
				create: (req) => createUserAgentDefinition(organizationId, req),
				update: (name, req) =>
					updateUserAgentDefinition(organizationId, name, req),
				remove: (name) => deleteUserAgentDefinition(organizationId, name),
			};
		case "organization":
			return {
				list: () => getOrganizationAgentDefinitions(organizationId),
				get: (name) => getOrganizationAgentDefinition(organizationId, name),
				create: (req) => createOrganizationAgentDefinition(organizationId, req),
				update: (name, req) =>
					updateOrganizationAgentDefinition(organizationId, name, req),
				remove: (name) =>
					deleteOrganizationAgentDefinition(organizationId, name),
			};
		case "project": {
			const { projectId } = scope;
			return {
				list: () => getProjectAgentDefinitions(organizationId, projectId),
				get: (name) =>
					getProjectAgentDefinition(organizationId, projectId, name),
				create: (req) =>
					createProjectAgentDefinition(organizationId, projectId, req),
				update: (name, req) =>
					updateProjectAgentDefinition(organizationId, projectId, name, req),
				remove: (name) =>
					deleteProjectAgentDefinition(organizationId, projectId, name),
			};
		}
	}
};

const agentDefinitionKey = (scope: AgentDefinitionScope, name: string) =>
	[...agentDefinitionsKey(scope), name] as const;

/** Query options for the agent definitions in a scope. */
export const agentDefinitions = (scope: AgentDefinitionScope) =>
	queryOptions({
		queryKey: agentDefinitionsKey(scope),
		queryFn: () => scopeEndpoints(scope).list(),
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
		queryKey: agentDefinitionKey(scope, name),
		queryFn: async () => {
			const listKey = agentDefinitions(scope).queryKey;
			const listed = queryClient
				.getQueryData(listKey)
				?.find((definition) => definition.name === name);
			try {
				return await scopeEndpoints(scope).get(name);
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
export const invalidateList = (
	queryClient: QueryClient,
	queryKey: QueryKey,
	{ exact, awaitRefetch }: { exact: boolean; awaitRefetch: boolean },
) => {
	const refetch = queryClient.invalidateQueries({ queryKey, exact });
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
			scopeEndpoints(scope).create(req),
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
			invalidateList(queryClient, agentDefinitionsKey(scope), {
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
			scopeEndpoints(scope).update(name, req),
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
			invalidateList(queryClient, agentDefinitionsKey(scope), {
				exact: false,
				awaitRefetch: isResponseLost(error),
			}),
	});

type DeleteAgentDefinitionArgs = {
	scope: AgentDefinitionScope;
	name: string;
};

/**
 * Mutation options that delete an agent definition. The list is invalidated
 * after failures too, because a 404 or a lost response can mean the
 * definition is already gone. A success also removes the row from the cache
 * so a failed refetch cannot leave it actionable.
 */
export const deleteAgentDefinition = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ scope, name }: DeleteAgentDefinitionArgs) =>
			scopeEndpoints(scope).remove(name),
		onSuccess: (_data, { scope, name }) => {
			queryClient.setQueryData(
				agentDefinitions(scope).queryKey,
				(definitions) =>
					definitions?.filter((definition) => definition.name !== name),
			);
		},
		onSettled: (_data, error, { scope }) =>
			invalidateList(queryClient, agentDefinitionsKey(scope), {
				exact: true,
				awaitRefetch:
					isResponseLost(error) ||
					(isAxiosError(error) && error.response?.status === 404),
			}),
	});

const organizationAgentDefinitionACLKey = (
	organizationId: string,
	name: string,
) =>
	[
		...agentDefinitionKey({ tier: "organization", organizationId }, name),
		"acl",
	] as const;

/** Query options for who can read an organization agent definition. */
export const organizationAgentDefinitionACL = (
	organizationId: string,
	name: string,
) =>
	queryOptions({
		queryKey: organizationAgentDefinitionACLKey(organizationId, name),
		queryFn: () => getOrganizationAgentDefinitionACL(organizationId, name),
	});

/**
 * Query options for the members and groups matching a sharing search. Plain
 * options, not queryOptions(), so ACLPrincipalAutocomplete accepts them.
 */
export const organizationAgentDefinitionACLAvailable = (
	organizationId: string,
	name: string,
	options: UsersRequest,
) => ({
	queryKey: [
		...organizationAgentDefinitionACLKey(organizationId, name),
		"available",
		options,
	] as const,
	queryFn: (): Promise<ACLAvailable> =>
		getOrganizationAgentDefinitionACLAvailable(organizationId, name, options),
});

type UpdateOrganizationAgentDefinitionACLArgs = {
	organizationId: string;
	name: string;
	req: UpdateOrganizationSkillACLRequest;
};

/**
 * Saves a sparse ACL change to an organization definition, the only tier
 * with an ACL.
 */
export const updateAgentDefinitionACL = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({
			organizationId,
			name,
			req,
		}: UpdateOrganizationAgentDefinitionACLArgs) =>
			updateOrganizationAgentDefinitionACL(organizationId, name, req),
		// Saving an ACL bumps the definition's updated_at, so its metadata is
		// stale.
		onSuccess: (_data, { organizationId, name }) => {
			const scope: AgentDefinitionScope = {
				tier: "organization",
				organizationId,
			};
			return Promise.all([
				queryClient.invalidateQueries({
					queryKey: organizationAgentDefinitionACLKey(organizationId, name),
					exact: true,
				}),
				queryClient.invalidateQueries({
					queryKey: agentDefinitionsKey(scope),
					exact: true,
				}),
				queryClient.invalidateQueries({
					queryKey: agentDefinitionKey(scope, name),
					exact: true,
				}),
			]);
		},
	});

/** Query options for the agent types a new chat in an organization can start as. */
export const agentTypes = (organizationId: string, params: AgentTypesParams) =>
	queryOptions({
		queryKey: ["chats", "fork-agent-types", organizationId, params] as const,
		queryFn: () => getAgentTypes(organizationId, params),
	});
