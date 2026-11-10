// This file is fork code: queries for the fork-agent-definitions experiment.

import { isAxiosError } from "axios";
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
 * invalidating the list refreshes an open editor's copy too. A 404 also
 * drops the listed row it was requested for from the cached list, so it is no
 * longer offered, unless a refetch has replaced that row since.
 */
export const userAgentDefinition = (
	queryClient: QueryClient,
	organizationId: string,
	name: string,
) =>
	queryOptions({
		queryKey: [...userAgentDefinitionsKey(organizationId), name] as const,
		queryFn: async () => {
			const listKey = userAgentDefinitions(organizationId).queryKey;
			const listed = queryClient
				.getQueryData(listKey)
				?.find((definition) => definition.name === name);
			try {
				return await getUserAgentDefinition(organizationId, name);
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

// API timestamps carry microseconds, which Date drops, so the fractional
// seconds are compared as digits.
const timestampParts = (value: string) => {
	const fraction = /\.(\d+)/.exec(value)?.[1] ?? "";
	return [
		Math.floor(new Date(value).getTime() / 1000),
		Number(fraction.padEnd(9, "0").slice(0, 9)),
	] as const;
};

/** Whether a was updated after b, to the timestamps' full precision. */
export const isNewer = (a: Timestamped, b: Timestamped) => {
	const [aSeconds, aNanos] = timestampParts(a.updated_at);
	const [bSeconds, bNanos] = timestampParts(b.updated_at);
	return aSeconds === bSeconds ? aNanos > bNanos : aSeconds > bSeconds;
};

/** Whether a request failed with no response, so it may still have applied. */
export const isResponseLost = (error: unknown) =>
	isAxiosError(error) && error.response === undefined;

// Callers reconcile an unknown outcome against the refetched list, so only
// then does the mutation stay pending until the refetch settles.
const invalidateList = (
	queryClient: QueryClient,
	organizationId: string,
	{ exact, awaitRefetch }: { exact: boolean; awaitRefetch: boolean },
) => {
	const refetch = queryClient.invalidateQueries({
		queryKey: userAgentDefinitionsKey(organizationId),
		exact,
	});
	return awaitRefetch ? refetch : undefined;
};

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
		// A list without the name may predate the commit or follow a delete;
		// only the refetch below can tell, so the created row is added until it
		// settles. The rows keep the API's order by name.
		onSuccess: ({ content: _content, ...created }, { organizationId }) => {
			queryClient.setQueryData(
				userAgentDefinitions(organizationId).queryKey,
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
		onSettled: (_data, error, { organizationId }) =>
			invalidateList(queryClient, organizationId, {
				exact: true,
				awaitRefetch: isResponseLost(error),
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
		// Rows match by name, as the request does, so a stale row of a
		// recreated definition takes the response. A response older than a
		// cached change does not replace it.
		onSuccess: (definition, { organizationId, name }) => {
			const { content: _content, ...updated } = definition;
			queryClient.setQueryData(
				userAgentDefinitions(organizationId).queryKey,
				(definitions) =>
					definitions?.map((listed) =>
						listed.name === updated.name && !isNewer(listed, updated)
							? updated
							: listed,
					),
			);
			queryClient.setQueryData(
				userAgentDefinition(queryClient, organizationId, name).queryKey,
				(cached) =>
					cached && isNewer(cached, definition) ? cached : definition,
			);
		},
		// Not exact, so a reopened editor refetches the definition it saved.
		onSettled: (_data, error, { organizationId }) =>
			invalidateList(queryClient, organizationId, {
				exact: false,
				awaitRefetch: isResponseLost(error),
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
		onSettled: (_data, error, { organizationId }) =>
			invalidateList(queryClient, organizationId, {
				exact: true,
				awaitRefetch:
					isResponseLost(error) ||
					(isAxiosError(error) && error.response?.status === 404),
			}),
	});
