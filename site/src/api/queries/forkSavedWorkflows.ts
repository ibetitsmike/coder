// This file is fork code: queries for the fork-workflows experiment's saved
// workflows.

import { isAxiosError } from "axios";
import {
	hashKey,
	mutationOptions,
	type QueryClient,
	queryOptions,
} from "react-query";
import type { UpdateSavedWorkflowRequest } from "#/api/forkSavedWorkflows";
import {
	createOrganizationSavedWorkflow,
	createUserSavedWorkflow,
	deleteOrganizationSavedWorkflow,
	deleteUserSavedWorkflow,
	getOrganizationSavedWorkflow,
	getOrganizationSavedWorkflowACL,
	getOrganizationSavedWorkflowACLAvailable,
	getOrganizationSavedWorkflows,
	getUserSavedWorkflow,
	getUserSavedWorkflows,
	updateOrganizationSavedWorkflow,
	updateOrganizationSavedWorkflowACL,
	updateUserSavedWorkflow,
} from "#/api/forkSavedWorkflows";
import type {
	ACLAvailable,
	CreateForkSavedWorkflowRequest,
	ForkSavedWorkflow,
	ForkSavedWorkflowMetadata,
	UpdateOrganizationSkillACLRequest,
	UsersRequest,
} from "#/api/typesGenerated";
import { invalidateList, isNewer, isResponseLost } from "./forkListCache";

/**
 * Where saved workflows live: a member's personal workflows in an
 * organization, or the organization's own.
 */
export type SavedWorkflowScope = {
	tier: "user" | "organization";
	organizationId: string;
};

/** Query key for the saved workflows in a scope. */
export const savedWorkflowsKey = (scope: SavedWorkflowScope) =>
	["chats", "fork-saved-workflows", scope.tier, scope.organizationId] as const;

/** Whether two scopes name the same workflows. */
export const isSameScope = (a: SavedWorkflowScope, b: SavedWorkflowScope) =>
	hashKey(savedWorkflowsKey(a)) === hashKey(savedWorkflowsKey(b));

type ScopeEndpoints = {
	list: () => Promise<ForkSavedWorkflowMetadata[]>;
	get: (name: string) => Promise<ForkSavedWorkflow>;
	create: (req: CreateForkSavedWorkflowRequest) => Promise<ForkSavedWorkflow>;
	update: (
		name: string,
		req: UpdateSavedWorkflowRequest,
	) => Promise<ForkSavedWorkflow>;
	remove: (name: string) => Promise<void>;
};

const scopeEndpoints = (scope: SavedWorkflowScope): ScopeEndpoints => {
	const { organizationId } = scope;
	switch (scope.tier) {
		case "user":
			return {
				list: () => getUserSavedWorkflows(organizationId),
				get: (name) => getUserSavedWorkflow(organizationId, name),
				create: (req) => createUserSavedWorkflow(organizationId, req),
				update: (name, req) =>
					updateUserSavedWorkflow(organizationId, name, req),
				remove: (name) => deleteUserSavedWorkflow(organizationId, name),
			};
		case "organization":
			return {
				list: () => getOrganizationSavedWorkflows(organizationId),
				get: (name) => getOrganizationSavedWorkflow(organizationId, name),
				create: (req) => createOrganizationSavedWorkflow(organizationId, req),
				update: (name, req) =>
					updateOrganizationSavedWorkflow(organizationId, name, req),
				remove: (name) => deleteOrganizationSavedWorkflow(organizationId, name),
			};
	}
};

/** Query key for one saved workflow in a scope. */
export const savedWorkflowKey = (scope: SavedWorkflowScope, name: string) =>
	[...savedWorkflowsKey(scope), name] as const;

/** Query options for the saved workflows in a scope. */
export const savedWorkflows = (scope: SavedWorkflowScope) =>
	queryOptions({
		queryKey: savedWorkflowsKey(scope),
		queryFn: () => scopeEndpoints(scope).list(),
	});

/**
 * The key extends the list key, so invalidating the list refetches this too.
 * A 404 drops the requested row from the cached list unless a refetch has
 * replaced that row since.
 */
export const savedWorkflow = (
	queryClient: QueryClient,
	scope: SavedWorkflowScope,
	name: string,
) =>
	queryOptions({
		queryKey: savedWorkflowKey(scope, name),
		queryFn: async () => {
			const listKey = savedWorkflows(scope).queryKey;
			const listed = queryClient
				.getQueryData(listKey)
				?.find((workflow) => workflow.name === name);
			try {
				return await scopeEndpoints(scope).get(name);
			} catch (error) {
				if (isAxiosError(error) && error.response?.status === 404 && listed) {
					queryClient.setQueryData(listKey, (workflows) =>
						workflows?.filter((workflow) => workflow !== listed),
					);
				}
				throw error;
			}
		},
	});

type CreateSavedWorkflowArgs = {
	scope: SavedWorkflowScope;
	req: CreateForkSavedWorkflowRequest;
};

/**
 * The list is invalidated after failures too, because a lost response can
 * hide a created workflow.
 */
export const createSavedWorkflow = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ scope, req }: CreateSavedWorkflowArgs) =>
			scopeEndpoints(scope).create(req),
		// The created row is added until the refetch settles: a list
		// without the name may predate the commit or follow a delete, and
		// only the refetch can tell. The rows keep the API's order by name.
		onSuccess: ({ script: _script, ...created }, { scope }) => {
			queryClient.setQueryData(savedWorkflows(scope).queryKey, (workflows) => {
				const listed = workflows?.find(
					(workflow) =>
						workflow.id === created.id || workflow.name === created.name,
				);
				if (!workflows || (listed && isNewer(listed, created))) {
					return workflows;
				}
				return [
					...workflows.filter((workflow) => workflow !== listed),
					created,
				].sort((a, b) => (a.name < b.name ? -1 : 1));
			});
		},
		onSettled: (_data, error, { scope }) =>
			invalidateList(queryClient, savedWorkflowsKey(scope), {
				exact: true,
				awaitRefetch: isResponseLost(error),
			}),
	});

type UpdateSavedWorkflowArgs = {
	scope: SavedWorkflowScope;
	name: string;
	req: UpdateSavedWorkflowRequest;
};

/**
 * Mutation options that update a saved workflow. The scope travels in the
 * variables so a request that settles after the user switches organizations
 * still invalidates its own list.
 */
export const updateSavedWorkflow = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ scope, name, req }: UpdateSavedWorkflowArgs) =>
			scopeEndpoints(scope).update(name, req),
		// Writing the result keeps an accepted change visible even if the
		// reconciling refetch below fails. Rows match by name, as the request
		// does, and a response older than a cached change does not replace it.
		onSuccess: (workflow, { scope, name }) => {
			const { script: _script, ...updated } = workflow;
			queryClient.setQueryData(savedWorkflows(scope).queryKey, (workflows) =>
				workflows?.map((listed) =>
					listed.name === updated.name && !isNewer(listed, updated)
						? updated
						: listed,
				),
			);
			queryClient.setQueryData(
				savedWorkflow(queryClient, scope, name).queryKey,
				(cached) => (cached && isNewer(cached, workflow) ? cached : workflow),
			);
		},
		// Not exact, so a reopened editor refetches the workflow it saved.
		onSettled: (_data, error, { scope }) =>
			invalidateList(queryClient, savedWorkflowsKey(scope), {
				exact: false,
				awaitRefetch: isResponseLost(error),
			}),
	});

type DeleteSavedWorkflowArgs = {
	scope: SavedWorkflowScope;
	name: string;
};

/**
 * Mutation options that delete a saved workflow. The list is invalidated
 * after failures too, because a 404 or a lost response can mean the workflow
 * is already gone. A success also removes the row's detail query, so a
 * failed refetch cannot leave it actionable.
 */
export const deleteSavedWorkflow = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ scope, name }: DeleteSavedWorkflowArgs) =>
			scopeEndpoints(scope).remove(name),
		onSuccess: (_data, { scope, name }) => {
			queryClient.setQueryData(savedWorkflows(scope).queryKey, (workflows) =>
				workflows?.filter((workflow) => workflow.name !== name),
			);
			queryClient.removeQueries({
				queryKey: savedWorkflowKey(scope, name),
				exact: true,
			});
		},
		onSettled: (_data, error, { scope }) =>
			invalidateList(queryClient, savedWorkflowsKey(scope), {
				exact: true,
				awaitRefetch:
					isResponseLost(error) ||
					(isAxiosError(error) && error.response?.status === 404),
			}),
	});

/** Query key for who can run one organization saved workflow. */
export const organizationSavedWorkflowACLKey = (
	organizationId: string,
	name: string,
) =>
	[
		...savedWorkflowKey({ tier: "organization", organizationId }, name),
		"acl",
	] as const;

/** Query options for who can read an organization saved workflow. */
export const organizationSavedWorkflowACL = (
	organizationId: string,
	name: string,
) =>
	queryOptions({
		queryKey: organizationSavedWorkflowACLKey(organizationId, name),
		queryFn: () => getOrganizationSavedWorkflowACL(organizationId, name),
	});

/**
 * Query options for the members and groups matching a sharing search. Plain
 * options, not queryOptions(), so ACLPrincipalAutocomplete accepts them.
 */
export const organizationSavedWorkflowACLAvailable = (
	organizationId: string,
	name: string,
	options: UsersRequest,
) => ({
	queryKey: [
		...organizationSavedWorkflowACLKey(organizationId, name),
		"available",
		options,
	] as const,
	queryFn: (): Promise<ACLAvailable> =>
		getOrganizationSavedWorkflowACLAvailable(organizationId, name, options),
});

type UpdateOrganizationSavedWorkflowACLArgs = {
	organizationId: string;
	name: string;
	req: UpdateOrganizationSkillACLRequest;
};

/**
 * Saves a sparse ACL change to an organization workflow, the only tier with
 * an ACL.
 */
export const updateSavedWorkflowACL = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({
			organizationId,
			name,
			req,
		}: UpdateOrganizationSavedWorkflowACLArgs) =>
			updateOrganizationSavedWorkflowACL(organizationId, name, req),
		// Saving an ACL bumps the workflow's updated_at, so its metadata is
		// stale.
		onSuccess: (_data, { organizationId, name }) => {
			const scope: SavedWorkflowScope = {
				tier: "organization",
				organizationId,
			};
			return Promise.all([
				queryClient.invalidateQueries({
					queryKey: organizationSavedWorkflowACLKey(organizationId, name),
					exact: true,
				}),
				queryClient.invalidateQueries({
					queryKey: savedWorkflowsKey(scope),
					exact: true,
				}),
				queryClient.invalidateQueries({
					queryKey: savedWorkflowKey(scope, name),
					exact: true,
				}),
			]);
		},
	});
