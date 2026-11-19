import { MutationObserver, QueryClient } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkSavedWorkflowsApi from "#/api/forkSavedWorkflows";
import type {
	ForkSavedWorkflow,
	ForkSavedWorkflowMetadata,
} from "#/api/typesGenerated";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockForkSavedWorkflow,
	MockForkSavedWorkflowMetadata,
} from "#/testHelpers/forkSavedWorkflows";
import {
	createSavedWorkflow,
	deleteSavedWorkflow,
	isSameScope,
	organizationSavedWorkflowACLKey,
	SavedWorkflowChangedError,
	type SavedWorkflowScope,
	savedWorkflow,
	savedWorkflowKey,
	savedWorkflowsKey,
	updateSavedWorkflow,
	updateSavedWorkflowACL,
} from "./forkSavedWorkflows";

const scope: SavedWorkflowScope = {
	tier: "user",
	organizationId: MockForkSavedWorkflowMetadata.organization_id,
};
const name = MockForkSavedWorkflowMetadata.name;

const MockForkSavedWorkflowCreated: ForkSavedWorkflow = {
	...MockForkSavedWorkflow,
	id: "saved-workflow-created",
	updated_at: "2030-01-01T00:00:00Z",
	script: 'def main(args):\n    log("Created.")\n',
};

const MockForkSavedWorkflowNewer: ForkSavedWorkflow = {
	...MockForkSavedWorkflowCreated,
	id: "saved-workflow-newer",
	updated_at: "2030-01-02T00:00:00Z",
};

const MockForkSavedWorkflowOlder: ForkSavedWorkflow = {
	...MockForkSavedWorkflowCreated,
	id: "saved-workflow-older",
	updated_at: "2029-01-01T00:00:00Z",
};

const MockForkSavedWorkflowDeleted: ForkSavedWorkflow = {
	...MockForkSavedWorkflowCreated,
	id: "saved-workflow-deleted",
	updated_at: "2028-01-01T00:00:00Z",
};

const MockForkSavedWorkflowRecreated: ForkSavedWorkflow = {
	...MockForkSavedWorkflowCreated,
	id: "saved-workflow-recreated",
	updated_at: "2030-01-01T00:00:00.000900Z",
};

const MockForkSavedWorkflowCreatedBeforeRecreate: ForkSavedWorkflow = {
	...MockForkSavedWorkflowCreated,
	updated_at: "2030-01-01T00:00:00.000450Z",
};

const metadataOf = ({
	script: _script,
	...metadata
}: ForkSavedWorkflow): ForkSavedWorkflowMetadata => metadata;

const mockCreateRequest = {
	name,
	description: MockForkSavedWorkflowMetadata.description,
	script: 'def main(args):\n    log("hi")\n',
};

// Mutation results are written with no refetch in flight to replace them.
const createQueryClient = () => {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	});
	vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
	return queryClient;
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("createSavedWorkflow", () => {
	it("creates an organization workflow in the organization's list only", async () => {
		const queryClient = createQueryClient();
		const organizationScope: SavedWorkflowScope = {
			...scope,
			tier: "organization",
		};
		queryClient.setQueryData(savedWorkflowsKey(scope), []);
		queryClient.setQueryData(savedWorkflowsKey(organizationScope), []);
		const created = MockForkSavedWorkflowCreated;
		const createOrganization = vi
			.spyOn(forkSavedWorkflowsApi, "createOrganizationSavedWorkflow")
			.mockResolvedValue(created);
		const createUser = vi.spyOn(
			forkSavedWorkflowsApi,
			"createUserSavedWorkflow",
		);

		await new MutationObserver(
			queryClient,
			createSavedWorkflow(queryClient),
		).mutate({ scope: organizationScope, req: mockCreateRequest });

		expect(createOrganization).toHaveBeenCalledWith(
			scope.organizationId,
			mockCreateRequest,
		);
		expect(createUser).not.toHaveBeenCalled();
		expect(
			queryClient.getQueryData(savedWorkflowsKey(organizationScope)),
		).toEqual([metadataOf(created)]);
		expect(queryClient.getQueryData(savedWorkflowsKey(scope))).toEqual([]);
	});

	it.each([
		["a later change", MockForkSavedWorkflowNewer],
		["a later recreate", MockForkSavedWorkflowRecreated],
	])("keeps a listed row that shows %s", async (_case, listed) => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		queryClient.setQueryData(listKey, [metadataOf(listed)]);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"createUserSavedWorkflow",
		).mockResolvedValue(MockForkSavedWorkflowCreatedBeforeRecreate);

		await new MutationObserver(
			queryClient,
			createSavedWorkflow(queryClient),
		).mutate({ scope, req: mockCreateRequest });

		expect(queryClient.getQueryData(listKey)).toEqual([metadataOf(listed)]);
	});

	it("replaces an older listed row of a deleted workflow with the created one", async () => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		queryClient.setQueryData(listKey, [
			metadataOf(MockForkSavedWorkflowDeleted),
		]);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"createUserSavedWorkflow",
		).mockResolvedValue(MockForkSavedWorkflowCreated);

		await new MutationObserver(
			queryClient,
			createSavedWorkflow(queryClient),
		).mutate({ scope, req: mockCreateRequest });

		expect(queryClient.getQueryData(listKey)).toEqual([
			metadataOf(MockForkSavedWorkflowCreated),
		]);
	});
});

describe("updateSavedWorkflow", () => {
	it("updates the listed row and the fetched workflow", async () => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		const detailKey = savedWorkflowKey(scope, name);
		queryClient.setQueryData(listKey, [metadataOf(MockForkSavedWorkflowOlder)]);
		queryClient.setQueryData(detailKey, MockForkSavedWorkflowOlder);
		const updated = MockForkSavedWorkflowCreated;
		vi.spyOn(
			forkSavedWorkflowsApi,
			"updateUserSavedWorkflow",
		).mockResolvedValue(updated);

		await new MutationObserver(
			queryClient,
			updateSavedWorkflow(queryClient),
		).mutate({ scope, name, req: { description: "New." } });

		expect(queryClient.getQueryData(listKey)).toEqual([metadataOf(updated)]);
		expect(queryClient.getQueryData(detailKey)).toEqual(updated);
	});

	it("keeps a cached change that is newer than the response", async () => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		const newer = MockForkSavedWorkflowNewer;
		queryClient.setQueryData(listKey, [metadataOf(newer)]);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"updateUserSavedWorkflow",
		).mockResolvedValue(MockForkSavedWorkflowCreated);

		await new MutationObserver(
			queryClient,
			updateSavedWorkflow(queryClient),
		).mutate({ scope, name, req: { description: "Old." } });

		expect(queryClient.getQueryData(listKey)).toEqual([metadataOf(newer)]);
	});

	it("settles a rejected update without waiting for the list refetch", async () => {
		const queryClient = createQueryClient();
		vi.mocked(queryClient.invalidateQueries).mockReturnValue(
			new Promise(() => {}),
		);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"updateUserSavedWorkflow",
		).mockRejectedValue(mockApiError({ message: "Invalid." }));

		await expect(
			new MutationObserver(
				queryClient,
				updateSavedWorkflow(queryClient),
			).mutate({ scope, name, req: { description: "New." } }),
		).rejects.toBeDefined();
	});
});

describe("deleteSavedWorkflow", () => {
	it("removes the workflow from the list and drops its detail query", async () => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		const detailKey = savedWorkflowKey(scope, name);
		queryClient.setQueryData(listKey, [
			metadataOf(MockForkSavedWorkflowCreated),
		]);
		queryClient.setQueryData(detailKey, MockForkSavedWorkflowCreated);
		vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockResolvedValue(
			MockForkSavedWorkflowCreated,
		);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"deleteUserSavedWorkflow",
		).mockResolvedValue();

		await new MutationObserver(
			queryClient,
			deleteSavedWorkflow(queryClient),
		).mutate({ scope, name, id: MockForkSavedWorkflowCreated.id });

		expect(forkSavedWorkflowsApi.deleteUserSavedWorkflow).toHaveBeenCalledWith(
			scope.organizationId,
			name,
		);
		expect(queryClient.getQueryData(listKey)).toEqual([]);
		expect(queryClient.getQueryData(detailKey)).toBeUndefined();
	});

	it.each([
		[
			"holds a recreated workflow",
			() => Promise.resolve(MockForkSavedWorkflowRecreated),
		],
		[
			"no longer exists",
			() =>
				Promise.reject(mockApiError({ message: "Not found.", status: 404 })),
		],
	])("deletes nothing when the name %s", async (_case, get) => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		const listed = [metadataOf(MockForkSavedWorkflowCreated)];
		queryClient.setQueryData(listKey, listed);
		vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockImplementation(
			get,
		);
		const remove = vi
			.spyOn(forkSavedWorkflowsApi, "deleteUserSavedWorkflow")
			.mockResolvedValue();

		await expect(
			new MutationObserver(
				queryClient,
				deleteSavedWorkflow(queryClient),
			).mutate({ scope, name, id: MockForkSavedWorkflowCreated.id }),
		).rejects.toBeInstanceOf(SavedWorkflowChangedError);

		expect(remove).not.toHaveBeenCalled();
		expect(queryClient.getQueryData(listKey)).toEqual(listed);
		expect(queryClient.invalidateQueries).toHaveBeenCalledWith({
			queryKey: listKey,
		});
	});

	it("keeps a same-name workflow cached while the delete was in flight", async () => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		const detailKey = savedWorkflowKey(scope, name);
		queryClient.setQueryData(listKey, [
			metadataOf(MockForkSavedWorkflowCreated),
		]);
		vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockResolvedValue(
			MockForkSavedWorkflowCreated,
		);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"deleteUserSavedWorkflow",
		).mockImplementation(async () => {
			queryClient.setQueryData(listKey, [
				metadataOf(MockForkSavedWorkflowRecreated),
			]);
			queryClient.setQueryData(detailKey, MockForkSavedWorkflowRecreated);
		});

		await new MutationObserver(
			queryClient,
			deleteSavedWorkflow(queryClient),
		).mutate({ scope, name, id: MockForkSavedWorkflowCreated.id });

		expect(queryClient.getQueryData(listKey)).toEqual([
			metadataOf(MockForkSavedWorkflowRecreated),
		]);
		expect(queryClient.getQueryData(detailKey)).toEqual(
			MockForkSavedWorkflowRecreated,
		);
	});

	it("reports a change when the workflow is gone by the time of the delete", async () => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		const listed = [metadataOf(MockForkSavedWorkflowCreated)];
		queryClient.setQueryData(listKey, listed);
		vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockResolvedValue(
			MockForkSavedWorkflowCreated,
		);
		vi.spyOn(
			forkSavedWorkflowsApi,
			"deleteUserSavedWorkflow",
		).mockRejectedValue(mockApiError({ message: "Not found.", status: 404 }));

		await expect(
			new MutationObserver(
				queryClient,
				deleteSavedWorkflow(queryClient),
			).mutate({ scope, name, id: MockForkSavedWorkflowCreated.id }),
		).rejects.toBeInstanceOf(SavedWorkflowChangedError);

		expect(queryClient.getQueryData(listKey)).toEqual(listed);
		expect(queryClient.invalidateQueries).toHaveBeenCalledWith({
			queryKey: listKey,
		});
	});
});

describe("savedWorkflow", () => {
	it("drops a listed row that 404s unless a refetch replaced it", async () => {
		const queryClient = createQueryClient();
		const listKey = savedWorkflowsKey(scope);
		queryClient.setQueryData(listKey, [
			metadataOf(MockForkSavedWorkflowCreated),
		]);
		vi.spyOn(forkSavedWorkflowsApi, "getUserSavedWorkflow").mockRejectedValue(
			mockApiError({ message: "Not found.", status: 404 }),
		);

		await expect(
			queryClient.fetchQuery(savedWorkflow(queryClient, scope, name)),
		).rejects.toThrow();
		expect(queryClient.getQueryData(listKey)).toEqual([]);
	});
});

describe("isSameScope", () => {
	it("treats the same tier and organization as the same scope", () => {
		expect(isSameScope(scope, { ...scope })).toBe(true);
		expect(isSameScope(scope, { ...scope, tier: "organization" })).toBe(false);
		expect(isSameScope(scope, { ...scope, organizationId: "other-org" })).toBe(
			false,
		);
	});
});

describe("updateSavedWorkflowACL", () => {
	it("invalidates the ACL, the list, and the workflow", async () => {
		const queryClient = createQueryClient();
		vi.spyOn(
			forkSavedWorkflowsApi,
			"updateOrganizationSavedWorkflowACL",
		).mockResolvedValue();
		const organizationScope: SavedWorkflowScope = {
			...scope,
			tier: "organization",
		};

		await new MutationObserver(
			queryClient,
			updateSavedWorkflowACL(queryClient),
		).mutate({
			organizationId: scope.organizationId,
			name,
			req: { user_roles: { [MockForkSavedWorkflowMetadata.id]: "read" } },
		});

		expect(queryClient.invalidateQueries).toHaveBeenCalledWith(
			expect.objectContaining({
				queryKey: organizationSavedWorkflowACLKey(scope.organizationId, name),
			}),
		);
		expect(queryClient.invalidateQueries).toHaveBeenCalledWith(
			expect.objectContaining({
				queryKey: savedWorkflowsKey(organizationScope),
			}),
		);
		expect(queryClient.invalidateQueries).toHaveBeenCalledWith(
			expect.objectContaining({
				queryKey: savedWorkflowKey(organizationScope, name),
			}),
		);
	});
});
