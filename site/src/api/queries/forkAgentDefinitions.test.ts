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
	createUserAgentDefinition,
	deleteUserAgentDefinition,
	isNewer,
	updateUserAgentDefinition,
	userAgentDefinition,
	userAgentDefinitions,
} from "./forkAgentDefinitions";

const org = MockForkAgentDefinitionMetadata.organization_id;
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

describe("isNewer", () => {
	it.each([
		[
			"microseconds",
			"2030-01-01T00:00:00.497900Z",
			"2030-01-01T00:00:00.497450Z",
		],
		[
			"trimmed zeros",
			"2030-01-01T00:00:00.49745Z",
			"2030-01-01T00:00:00.4974Z",
		],
		["no fraction", "2030-01-01T00:00:01Z", "2030-01-01T00:00:00.999999Z"],
		["offsets", "2030-01-01T01:00:00.5+01:00", "2030-01-01T00:00:00.4Z"],
	])("orders timestamps with %s", (_case, later, earlier) => {
		expect(isNewer({ updated_at: later }, { updated_at: earlier })).toBe(true);
		expect(isNewer({ updated_at: earlier }, { updated_at: later })).toBe(false);
		expect(isNewer({ updated_at: later }, { updated_at: later })).toBe(false);
	});
});

describe("createUserAgentDefinition", () => {
	it.each([
		["a later change", definitionAt("2030-01-01T00:00:00.000900Z")],
		["a recreated definition", definitionAt("2029-01-01T00:00:00Z", "other")],
	])("keeps a listed row that shows %s", async (_case, listed) => {
		const queryClient = createQueryClient();
		const listKey = userAgentDefinitions(org).queryKey;
		queryClient.setQueryData(listKey, [metadataOf(listed)]);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"createUserAgentDefinition",
		).mockResolvedValue(definitionAt("2030-01-01T00:00:00.000450Z"));

		await new MutationObserver(
			queryClient,
			createUserAgentDefinition(queryClient),
		).mutate({ organizationId: org, req: { content: "" } });

		expect(queryClient.getQueryData(listKey)).toEqual([metadataOf(listed)]);
	});

	it("adds a created definition in name order to a list fetched during the request", async () => {
		const queryClient = createQueryClient();
		const listKey = userAgentDefinitions(org).queryKey;
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
			createUserAgentDefinition(queryClient),
		).mutate({ organizationId: org, req: { content: "" } });

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
					createUserAgentDefinition(queryClient),
				).mutate({ organizationId: org, req: { content: "" } });
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
					updateUserAgentDefinition(queryClient),
				).mutate({ organizationId: org, name, req: { content: "" } });
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
					deleteUserAgentDefinition(queryClient),
				).mutate({ organizationId: org, name });
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

describe("updateUserAgentDefinition", () => {
	it("replaces a stale row with the recreated definition the update returns", async () => {
		const queryClient = createQueryClient();
		const listKey = userAgentDefinitions(org).queryKey;
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
			updateUserAgentDefinition(queryClient),
		).mutate({ organizationId: org, name, req: { enabled: false } });

		expect(queryClient.getQueryData(listKey)).toEqual([
			metadataOf(MockRecreatedDefinition),
		]);
	});

	it("keeps a newer cached detail of a recreated definition", async () => {
		const queryClient = createQueryClient();
		const detailKey = userAgentDefinition(queryClient, org, name).queryKey;
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
			updateUserAgentDefinition(queryClient),
		).mutate({ organizationId: org, name, req: { enabled: false } });

		expect(queryClient.getQueryData(detailKey)).toEqual(
			MockRecreatedDefinition,
		);
	});
});

describe("userAgentDefinition", () => {
	it("drops a definition the server no longer has from the cached list", async () => {
		const queryClient = createQueryClient();
		const listKey = userAgentDefinitions(org).queryKey;
		queryClient.setQueryData(listKey, [MockForkAgentDefinitionMetadata]);
		vi.spyOn(
			forkAgentDefinitionsApi,
			"getUserAgentDefinition",
		).mockRejectedValue({
			isAxiosError: true,
			response: { status: 404, data: { message: "Not found." } },
		});

		await expect(
			queryClient.fetchQuery(userAgentDefinition(queryClient, org, name)),
		).rejects.toBeDefined();

		expect(queryClient.getQueryData(listKey)).toEqual([]);
	});

	it("keeps a row a refetch replaced while the 404 was in flight", async () => {
		const queryClient = createQueryClient();
		const listKey = userAgentDefinitions(org).queryKey;
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
			queryClient.fetchQuery(userAgentDefinition(queryClient, org, name)),
		).rejects.toBeDefined();

		expect(queryClient.getQueryData(listKey)).toEqual([MockRecreatedMetadata]);
	});
});
