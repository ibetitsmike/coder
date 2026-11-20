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
	shareUserSavedWorkflow,
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
import { isNewer } from "./forkListCache";

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

/** Mutation options that create a saved workflow in a scope. */
export const createSavedWorkflow = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ scope, req }: CreateSavedWorkflowArgs) =>
			scopeEndpoints(scope).create(req),
		// The created row is added until the refetch settles: a list
		// without the name may predate the commit or follow a delete, and
		// only the refetch can tell. The rows keep the API's order by name.
		onSuccess: (workflow, { scope }) => {
			// A detail cached under the name may hold an older workflow of that
			// name, so the full response replaces it unless it is newer.
			queryClient.setQueryData<ForkSavedWorkflow>(
				savedWorkflowKey(scope, workflow.name),
				(cached) => (cached && isNewer(cached, workflow) ? cached : workflow),
			);
			const { script: _script, ...created } = workflow;
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
		onSettled: (_data, _error, { scope }) => {
			void queryClient.invalidateQueries({
				queryKey: savedWorkflowsKey(scope),
				exact: true,
			});
		},
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
		onSettled: (_data, _error, { scope }) => {
			void queryClient.invalidateQueries({
				queryKey: savedWorkflowsKey(scope),
			});
		},
	});

type DeleteSavedWorkflowArgs = {
	scope: SavedWorkflowScope;
	name: string;
	/** The workflow the user picked, which the name must still resolve to. */
	id: string;
};

/**
 * Thrown when the name of a workflow picked for deletion no longer resolves
 * to that workflow, because it was deleted or recreated meanwhile.
 */
export class SavedWorkflowChangedError extends Error {
	constructor() {
		super("The saved workflow was deleted or replaced.");
		this.name = "SavedWorkflowChangedError";
	}
}

/**
 * Mutation options that delete a saved workflow. The API deletes by name, so
 * the request first checks that the name still resolves to the picked
 * workflow; otherwise it would delete one the user never chose. A success
 * also removes the row's detail query, so a failed refetch cannot leave it
 * actionable.
 */
export const deleteSavedWorkflow = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: async ({ scope, name, id }: DeleteSavedWorkflowArgs) => {
			const endpoints = scopeEndpoints(scope);
			let current: ForkSavedWorkflow;
			try {
				current = await endpoints.get(name);
			} catch (error) {
				if (isAxiosError(error) && error.response?.status === 404) {
					throw new SavedWorkflowChangedError();
				}
				throw error;
			}
			if (current.id !== id) {
				throw new SavedWorkflowChangedError();
			}
			try {
				await endpoints.remove(name);
			} catch (error) {
				// Someone else deleted the workflow after the check.
				if (isAxiosError(error) && error.response?.status === 404) {
					throw new SavedWorkflowChangedError();
				}
				throw error;
			}
		},
		// Match by id, so a same-name workflow cached after the delete started
		// stays.
		onSuccess: (_data, { scope, name, id }) => {
			queryClient.setQueryData(savedWorkflows(scope).queryKey, (workflows) =>
				workflows?.filter((workflow) => workflow.id !== id),
			);
			const detailKey = savedWorkflowKey(scope, name);
			const cached = queryClient.getQueryData<ForkSavedWorkflow>(detailKey);
			if (cached === undefined || cached.id === id) {
				queryClient.removeQueries({ queryKey: detailKey, exact: true });
			}
		},
		onSettled: (_data, _error, { scope }) => {
			void queryClient.invalidateQueries({
				queryKey: savedWorkflowsKey(scope),
			});
		},
	});

type ShareSavedWorkflowArgs = {
	organizationId: string;
	name: string;
	/** The personal workflow the user picked, which the name must still hold. */
	id: string;
};

/**
 * Mutation options that move a personal workflow to the organization. The
 * server rejects the move with 409 when the name holds another personal
 * workflow, the organization has a workflow of that name, or it is full.
 */
export const shareSavedWorkflow = (queryClient: QueryClient) =>
	mutationOptions({
		mutationFn: ({ organizationId, name, id }: ShareSavedWorkflowArgs) =>
			shareUserSavedWorkflow(organizationId, name, id),
		onSuccess: (_data, { organizationId, name, id }) => {
			const scope: SavedWorkflowScope = { tier: "user", organizationId };
			queryClient.setQueryData(savedWorkflows(scope).queryKey, (workflows) =>
				workflows?.filter((workflow) => workflow.id !== id),
			);
			const detailKey = savedWorkflowKey(scope, name);
			const cached = queryClient.getQueryData<ForkSavedWorkflow>(detailKey);
			if (cached === undefined || cached.id === id) {
				queryClient.removeQueries({ queryKey: detailKey, exact: true });
			}
		},
		// Both lists change: the row leaves the personal one and joins the
		// organization one.
		onSettled: (_data, _error, { organizationId }) => {
			for (const tier of ["user", "organization"] as const) {
				void queryClient.invalidateQueries({
					queryKey: savedWorkflowsKey({ tier, organizationId }),
				});
			}
		},
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
