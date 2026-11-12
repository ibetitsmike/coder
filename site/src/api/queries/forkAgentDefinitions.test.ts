import { MutationObserver, QueryClient } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import type {
	ForkAgentDefinition,
	ForkAgentDefinitionMetadata,
} from "#/api/typesGenerated";
import { mockApiError } from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import {
	type AgentDefinitionScope,
	agentDefinition,
	agentDefinitions,
	createAgentDefinition,
	deleteAgentDefinition,
	isSameScope,
	organizationAgentDefinitionACL,
	updateAgentDefinition,
	updateAgentDefinitionACL,
} from "./forkAgentDefinitions";

const scope: AgentDefinitionScope = {
	tier: "user",
	organizationId: MockForkAgentDefinitionMetadata.organization_id,
};
const name = MockForkAgentDefinitionMetadata.name;

const definitionAt = (
	updatedAt: string,
	id = MockForkAgentDefinitionMetadata.id,
): ForkAgentDefinition => ({
	...MockForkAgentDefinitionMetadata,
	id,
	updated_at: updatedAt,
	content: `---\nname: ${name}\n---\nUpdated at ${updatedAt}.\n`,
});

const metadataOf = ({
	content: _content,
	...metadata
}: ForkAgentDefinition): ForkAgentDefinitionMetadata => metadata;

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

describe("createAgentDefinition", () => {
	it("creates an organization definition in the organization's list only", async () => {
		const queryClient = createQueryClient();
		const organizationScope: AgentDefinitionScope = {
			...scope,
			tier: "organization",
		};
		queryClient.setQueryData(agentDefinitions(scope).queryKey, []);
		queryClient.setQueryData(agentDefinitions(organizationScope).queryKey, []);
		const created = definitionAt("2030-01-01T00:00:00Z");
		const createOrganization = vi
			.spyOn(forkAgentDefinitionsApi, "createOrganizationAgentDefinition")
			.mockResolvedValue(created);
		const createUser = vi.spyOn(
			forkAgentDefinitionsApi,
			"createUserAgentDefinition",
		);

		await new MutationObserver(
			queryClient,
			createAgentDefinition(queryClient),
		).mutate({ scope: organizationScope, req: { content: "" } });

		expect(createOrganization).toHaveBeenCalledWith(scope.organizationId, {
			content: "",
		});
		expect(createUser).not.toHaveBeenCalled();
		expect(
			queryClient.getQueryData(agentDefinitions(organizationScope).queryKey),
		).toEqual([metadataOf(created)]);
		expect(queryClient.getQueryData(agentDefinitions(scope).queryKey)).toEqual(
			[],
		);
	});

	it.each([
		["a later change", definitionAt("2030-01-01T00:00:00.000900Z")],
		["a later recreate", definitionAt("2030-01-01T00:00:00.000900Z", "other")],
	])("keeps a listed row that shows %s", async (_case, listed) => {
		const queryClient = createQueryClient();
		const listKey = agentDefinitions(scope).queryKey;
		queryClient.setQueryData(listKey, [metadataOf(listed)]);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"createUserAgentDefinition",
		).mockResolvedValue(definitionAt("2030-01-01T00:00:00.000450Z"));

		await new MutationObserver(
			queryClient,
			createAgentDefinition(queryClient),
		).mutate({ scope, req: { content: "" } });

		expect(queryClient.getQueryData(listKey)).toEqual([metadataOf(listed)]);
	});

	it("replaces an older listed row of a deleted definition with the created one", async () => {
		const queryClient = createQueryClient();
		const listKey = agentDefinitions(scope).queryKey;
		queryClient.setQueryData(listKey, [
			metadataOf(definitionAt("2029-01-01T00:00:00Z", "deleted")),
		]);
		const MockCreatedDefinition = definitionAt("2030-01-01T00:00:00Z");
		vi.spyOn(
			forkAgentDefinitionsApi,
			"createUserAgentDefinition",
		).mockResolvedValue(MockCreatedDefinition);

		await new MutationObserver(
			queryClient,
			createAgentDefinition(queryClient),
		).mutate({ scope, req: { content: "" } });

		expect(queryClient.getQueryData(listKey)).toEqual([
			metadataOf(MockCreatedDefinition),
		]);
	});

	it("adds a created definition in name order to a list fetched during the request", async () => {
		const queryClient = createQueryClient();
		const listKey = agentDefinitions(scope).queryKey;
		const MockLaterMetadata = {
			...metadataOf(definitionAt("2029-01-01T00:00:00Z", "later")),
			name: "z-reviewer",
		};
		vi.spyOn(
			forkAgentDefinitionsApi,
			"createUserAgentDefinition",
		).mockImplementation(async () => {
			queryClient.setQueryData(listKey, [MockLaterMetadata]);
			return {
				...definitionAt("2030-01-01T00:00:00Z", "new"),
				name: "a-reviewer",
			};
		});

		await new MutationObserver(
			queryClient,
			createAgentDefinition(queryClient),
		).mutate({ scope, req: { content: "" } });

		expect(
			queryClient.getQueryData(listKey)?.map((definition) => definition.name),
		).toEqual(["a-reviewer", "z-reviewer"]);
	});
});

describe("agent definition mutations", () => {
	it.each([
		[
			"create",
			(queryClient: QueryClient) => {
				vi.spyOn(
					forkAgentDefinitionsApi,
					"createUserAgentDefinition",
				).mockRejectedValue(mockApiError({ message: "Name taken." }));
				return new MutationObserver(
					queryClient,
					createAgentDefinition(queryClient),
				).mutate({ scope, req: { content: "" } });
			},
		],
		[
			"update",
			(queryClient: QueryClient) => {
				vi.spyOn(
					forkAgentDefinitionsApi,
					"updateUserAgentDefinition",
				).mockRejectedValue(mockApiError({ message: "Invalid." }));
				return new MutationObserver(
					queryClient,
					updateAgentDefinition(queryClient),
				).mutate({ scope, name, req: { content: "" } });
			},
		],
		[
			"delete",
			(queryClient: QueryClient) => {
				vi.spyOn(
					forkAgentDefinitionsApi,
					"deleteUserAgentDefinition",
				).mockRejectedValue(mockApiError({ message: "Forbidden." }));
				return new MutationObserver(
					queryClient,
					deleteAgentDefinition(queryClient),
				).mutate({ scope, name });
			},
		],
	])(
		"settles a rejected %s without waiting for the list refetch",
		async (_case, mutate) => {
			const queryClient = createQueryClient();
			vi.mocked(queryClient.invalidateQueries).mockReturnValue(
				new Promise(() => {}),
			);

			await expect(mutate(queryClient)).rejects.toBeDefined();
		},
	);
});

describe("updateAgentDefinition", () => {
	it("replaces a stale row with the recreated definition the update returns", async () => {
		const queryClient = createQueryClient();
		const listKey = agentDefinitions(scope).queryKey;
		queryClient.setQueryData(listKey, [
			metadataOf(definitionAt("2029-01-01T00:00:00Z")),
		]);
		const MockRecreatedDefinition = definitionAt(
			"2030-01-01T00:00:00Z",
			"recreated",
		);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"updateUserAgentDefinition",
		).mockResolvedValue(MockRecreatedDefinition);

		await new MutationObserver(
			queryClient,
			updateAgentDefinition(queryClient),
		).mutate({ scope, name, req: { enabled: false } });

		expect(queryClient.getQueryData(listKey)).toEqual([
			metadataOf(MockRecreatedDefinition),
		]);
	});

	it("keeps a newer cached detail of a recreated definition", async () => {
		const queryClient = createQueryClient();
		const detailKey = agentDefinition(queryClient, scope, name).queryKey;
		const MockRecreatedDefinition = definitionAt(
			"2030-01-01T00:00:00Z",
			"recreated",
		);
		queryClient.setQueryData(detailKey, MockRecreatedDefinition);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"updateUserAgentDefinition",
		).mockResolvedValue(definitionAt("2029-01-01T00:00:00Z"));

		await new MutationObserver(
			queryClient,
			updateAgentDefinition(queryClient),
		).mutate({ scope, name, req: { enabled: false } });

		expect(queryClient.getQueryData(detailKey)).toEqual(
			MockRecreatedDefinition,
		);
	});
});

describe("agentDefinition", () => {
	it("drops a definition the server no longer has from the cached list", async () => {
		const queryClient = createQueryClient();
		const listKey = agentDefinitions(scope).queryKey;
		queryClient.setQueryData(listKey, [MockForkAgentDefinitionMetadata]);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"getUserAgentDefinition",
		).mockRejectedValue({
			isAxiosError: true,
			response: { status: 404, data: { message: "Not found." } },
		});

		await expect(
			queryClient.fetchQuery(agentDefinition(queryClient, scope, name)),
		).rejects.toBeDefined();

		expect(queryClient.getQueryData(listKey)).toEqual([]);
	});

	it("keeps a row a refetch replaced while the 404 was in flight", async () => {
		const queryClient = createQueryClient();
		const listKey = agentDefinitions(scope).queryKey;
		queryClient.setQueryData(listKey, [MockForkAgentDefinitionMetadata]);
		const MockRecreatedMetadata = metadataOf(
			definitionAt("2030-01-01T00:00:00Z", "recreated"),
		);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"getUserAgentDefinition",
		).mockImplementation(async () => {
			queryClient.setQueryData(listKey, [MockRecreatedMetadata]);
			throw {
				isAxiosError: true,
				response: { status: 404, data: { message: "Not found." } },
			};
		});

		await expect(
			queryClient.fetchQuery(agentDefinition(queryClient, scope, name)),
		).rejects.toBeDefined();

		expect(queryClient.getQueryData(listKey)).toEqual([MockRecreatedMetadata]);
	});
});

describe("organization scope", () => {
	const organizationScope: AgentDefinitionScope = {
		...scope,
		tier: "organization",
	};
	const listed = definitionAt("2029-01-01T00:00:00Z");

	it("lists and reads definitions from the organization endpoints", async () => {
		const queryClient = createQueryClient();
		const listOrganization = vi
			.spyOn(forkAgentDefinitionsApi, "getOrganizationAgentDefinitions")
			.mockResolvedValue([metadataOf(listed)]);
		const getOrganization = vi
			.spyOn(forkAgentDefinitionsApi, "getOrganizationAgentDefinition")
			.mockResolvedValue(listed);
		const listUser = vi.spyOn(
			forkAgentDefinitionsApi,
			"getUserAgentDefinitions",
		);
		const getUser = vi.spyOn(forkAgentDefinitionsApi, "getUserAgentDefinition");

		await queryClient.fetchQuery(agentDefinitions(organizationScope));
		await queryClient.fetchQuery(
			agentDefinition(queryClient, organizationScope, name),
		);

		expect(listOrganization).toHaveBeenCalledWith(scope.organizationId);
		expect(getOrganization).toHaveBeenCalledWith(scope.organizationId, name);
		expect(listUser).not.toHaveBeenCalled();
		expect(getUser).not.toHaveBeenCalled();
		expect(
			queryClient.getQueryData(agentDefinitions(scope).queryKey),
		).toBeUndefined();
	});

	it("updates the organization definition and leaves a same-named personal one", async () => {
		const queryClient = createQueryClient();
		queryClient.setQueryData(agentDefinitions(scope).queryKey, [
			metadataOf(listed),
		]);
		queryClient.setQueryData(agentDefinitions(organizationScope).queryKey, [
			metadataOf(listed),
		]);
		const updated = definitionAt("2030-01-01T00:00:00Z");
		const updateOrganization = vi
			.spyOn(forkAgentDefinitionsApi, "updateOrganizationAgentDefinition")
			.mockResolvedValue(updated);
		const updateUser = vi.spyOn(
			forkAgentDefinitionsApi,
			"updateUserAgentDefinition",
		);

		await new MutationObserver(
			queryClient,
			updateAgentDefinition(queryClient),
		).mutate({ scope: organizationScope, name, req: { enabled: false } });

		expect(updateOrganization).toHaveBeenCalledWith(
			scope.organizationId,
			name,
			{ enabled: false },
		);
		expect(updateUser).not.toHaveBeenCalled();
		expect(
			queryClient.getQueryData(agentDefinitions(organizationScope).queryKey),
		).toEqual([metadataOf(updated)]);
		expect(
			queryClient.getQueryData(
				agentDefinition(queryClient, organizationScope, name).queryKey,
			),
		).toEqual(updated);
		expect(queryClient.getQueryData(agentDefinitions(scope).queryKey)).toEqual([
			metadataOf(listed),
		]);
		expect(
			queryClient.getQueryData(
				agentDefinition(queryClient, scope, name).queryKey,
			),
		).toBeUndefined();
	});

	it("deletes the organization definition and leaves a same-named personal one", async () => {
		const queryClient = createQueryClient();
		queryClient.setQueryData(agentDefinitions(scope).queryKey, [
			metadataOf(listed),
		]);
		queryClient.setQueryData(agentDefinitions(organizationScope).queryKey, [
			metadataOf(listed),
		]);
		const deleteOrganization = vi
			.spyOn(forkAgentDefinitionsApi, "deleteOrganizationAgentDefinition")
			.mockResolvedValue();
		const deleteUser = vi.spyOn(
			forkAgentDefinitionsApi,
			"deleteUserAgentDefinition",
		);

		await new MutationObserver(
			queryClient,
			deleteAgentDefinition(queryClient),
		).mutate({ scope: organizationScope, name });

		expect(deleteOrganization).toHaveBeenCalledWith(scope.organizationId, name);
		expect(deleteUser).not.toHaveBeenCalled();
		expect(
			queryClient.getQueryData(agentDefinitions(organizationScope).queryKey),
		).toEqual([]);
		expect(queryClient.getQueryData(agentDefinitions(scope).queryKey)).toEqual([
			metadataOf(listed),
		]);
	});
});

describe("updateAgentDefinitionACL", () => {
	it("marks the saved definition's ACL, list, and detail stale", async () => {
		const queryClient = new QueryClient();
		const organizationScope: AgentDefinitionScope = {
			tier: "organization",
			organizationId: scope.organizationId,
		};
		const aclKey = organizationAgentDefinitionACL(
			scope.organizationId,
			name,
		).queryKey;
		const listKey = agentDefinitions(organizationScope).queryKey;
		const detailKey = agentDefinition(
			queryClient,
			organizationScope,
			name,
		).queryKey;
		const userListKey = agentDefinitions(scope).queryKey;
		queryClient.setQueryData(aclKey, { users: [], groups: [] });
		queryClient.setQueryData(listKey, [MockForkAgentDefinitionMetadata]);
		queryClient.setQueryData(detailKey, definitionAt("2030-01-01T00:00:00Z"));
		queryClient.setQueryData(userListKey, [MockForkAgentDefinitionMetadata]);
		const update = vi
			.spyOn(forkAgentDefinitionsApi, "updateOrganizationAgentDefinitionACL")
			.mockResolvedValue();

		await new MutationObserver(
			queryClient,
			updateAgentDefinitionACL(queryClient),
		).mutate({
			organizationId: scope.organizationId,
			name,
			req: { group_roles: { group: "read" } },
		});

		expect(update).toHaveBeenCalledWith(scope.organizationId, name, {
			group_roles: { group: "read" },
		});
		for (const key of [aclKey, listKey, detailKey]) {
			expect(queryClient.getQueryState(key)?.isInvalidated).toBe(true);
		}
		expect(queryClient.getQueryState(userListKey)?.isInvalidated).toBe(false);
	});
});

describe("project scope", () => {
	const projectScope: AgentDefinitionScope = {
		tier: "project",
		organizationId: scope.organizationId,
		projectId: "project-1",
	};
	const otherProjectScope: AgentDefinitionScope = {
		...projectScope,
		projectId: "project-2",
	};
	const listed = definitionAt("2029-01-01T00:00:00Z");

	it("names a different scope for each project", () => {
		expect(isSameScope(projectScope, { ...projectScope })).toBe(true);
		expect(isSameScope(projectScope, otherProjectScope)).toBe(false);
		expect(isSameScope(projectScope, { ...scope, tier: "organization" })).toBe(
			false,
		);
	});

	it("lists, reads, and creates definitions through the project endpoints", async () => {
		const queryClient = createQueryClient();
		const listProject = vi
			.spyOn(forkAgentDefinitionsApi, "getProjectAgentDefinitions")
			.mockResolvedValue([]);
		const getProject = vi
			.spyOn(forkAgentDefinitionsApi, "getProjectAgentDefinition")
			.mockResolvedValue(listed);
		const createProject = vi
			.spyOn(forkAgentDefinitionsApi, "createProjectAgentDefinition")
			.mockResolvedValue(listed);
		queryClient.setQueryData(agentDefinitions(otherProjectScope).queryKey, []);

		await queryClient.fetchQuery(agentDefinitions(projectScope));
		await queryClient.fetchQuery(
			agentDefinition(queryClient, projectScope, name),
		);
		await new MutationObserver(
			queryClient,
			createAgentDefinition(queryClient),
		).mutate({ scope: projectScope, req: { content: "" } });

		expect(listProject).toHaveBeenCalledWith(scope.organizationId, "project-1");
		expect(getProject).toHaveBeenCalledWith(
			scope.organizationId,
			"project-1",
			name,
		);
		expect(createProject).toHaveBeenCalledWith(
			scope.organizationId,
			"project-1",
			{ content: "" },
		);
		expect(
			queryClient.getQueryData(agentDefinitions(projectScope).queryKey),
		).toEqual([metadataOf(listed)]);
		expect(
			queryClient.getQueryData(agentDefinitions(otherProjectScope).queryKey),
		).toEqual([]);
	});

	it("updates and deletes the project definition and leaves another project's", async () => {
		const queryClient = createQueryClient();
		for (const listScope of [projectScope, otherProjectScope]) {
			queryClient.setQueryData(agentDefinitions(listScope).queryKey, [
				metadataOf(listed),
			]);
		}
		const updated = definitionAt("2030-01-01T00:00:00Z");
		const updateProject = vi
			.spyOn(forkAgentDefinitionsApi, "updateProjectAgentDefinition")
			.mockResolvedValue(updated);
		const deleteProject = vi
			.spyOn(forkAgentDefinitionsApi, "deleteProjectAgentDefinition")
			.mockResolvedValue();

		await new MutationObserver(
			queryClient,
			updateAgentDefinition(queryClient),
		).mutate({ scope: projectScope, name, req: { enabled: false } });
		expect(updateProject).toHaveBeenCalledWith(
			scope.organizationId,
			"project-1",
			name,
			{ enabled: false },
		);
		expect(
			queryClient.getQueryData(agentDefinitions(projectScope).queryKey),
		).toEqual([metadataOf(updated)]);

		await new MutationObserver(
			queryClient,
			deleteAgentDefinition(queryClient),
		).mutate({ scope: projectScope, name });
		expect(deleteProject).toHaveBeenCalledWith(
			scope.organizationId,
			"project-1",
			name,
		);
		expect(
			queryClient.getQueryData(agentDefinitions(projectScope).queryKey),
		).toEqual([]);
		expect(
			queryClient.getQueryData(agentDefinitions(otherProjectScope).queryKey),
		).toEqual([metadataOf(listed)]);
	});
});
