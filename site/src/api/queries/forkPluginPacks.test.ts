import { MutationObserver, QueryClient } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkPluginPacksApi from "#/api/forkPluginPacks";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import {
	createPluginPackMutation,
	pluginPacksKey,
	updatePluginPackMutation,
} from "./forkPluginPacks";

const organizationId = MockForkPluginPack.organization_id;
const listKey = pluginPacksKey(organizationId);

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

describe("createPluginPackMutation", () => {
	const req = {
		name: MockForkPluginPack.name,
		source_url: MockForkPluginPack.source_url,
		ref: MockForkPluginPack.ref,
	};

	it("keeps the created pack when no list is cached", async () => {
		const queryClient = createQueryClient();
		vi.spyOn(forkPluginPacksApi, "createPluginPack").mockResolvedValue(
			MockForkPluginPack,
		);

		await new MutationObserver(
			queryClient,
			createPluginPackMutation(queryClient, organizationId),
		).mutate(req);

		expect(queryClient.getQueryData(listKey)).toEqual([MockForkPluginPack]);
	});

	it("adds the created pack to the cached list in name order", async () => {
		const queryClient = createQueryClient();
		queryClient.setQueryData(listKey, [MockForkPluginPackWithoutPath]);
		vi.spyOn(forkPluginPacksApi, "createPluginPack").mockResolvedValue(
			MockForkPluginPack,
		);

		await new MutationObserver(
			queryClient,
			createPluginPackMutation(queryClient, organizationId),
		).mutate(req);

		expect(queryClient.getQueryData(listKey)).toEqual([
			MockForkPluginPack,
			MockForkPluginPackWithoutPath,
		]);
	});

	it("replaces a cached pack that has the same name", async () => {
		const queryClient = createQueryClient();
		const mockRecreatedPack = { ...MockForkPluginPack, id: "recreated-pack" };
		queryClient.setQueryData(listKey, [MockForkPluginPack]);
		vi.spyOn(forkPluginPacksApi, "createPluginPack").mockResolvedValue(
			mockRecreatedPack,
		);

		await new MutationObserver(
			queryClient,
			createPluginPackMutation(queryClient, organizationId),
		).mutate(req);

		expect(queryClient.getQueryData(listKey)).toEqual([mockRecreatedPack]);
	});
});

describe("updatePluginPackMutation", () => {
	const mockNewerPack = {
		...MockForkPluginPack,
		commit_sha: "fedcba9876543210fedcba9876543210fedcba98",
	};

	it("leaves an uncached list uncached", async () => {
		const queryClient = createQueryClient();
		vi.spyOn(forkPluginPacksApi, "updatePluginPack").mockResolvedValue(
			mockNewerPack,
		);

		await new MutationObserver(
			queryClient,
			updatePluginPackMutation(queryClient, organizationId),
		).mutate(MockForkPluginPack);

		expect(queryClient.getQueryData(listKey)).toBeUndefined();
	});

	it("replaces the cached copy of the newer pack", async () => {
		const queryClient = createQueryClient();
		queryClient.setQueryData(listKey, [
			MockForkPluginPack,
			MockForkPluginPackWithoutPath,
		]);
		vi.spyOn(forkPluginPacksApi, "updatePluginPack").mockResolvedValue(
			mockNewerPack,
		);

		await new MutationObserver(
			queryClient,
			updatePluginPackMutation(queryClient, organizationId),
		).mutate(MockForkPluginPack);

		expect(queryClient.getQueryData(listKey)).toEqual([
			mockNewerPack,
			MockForkPluginPackWithoutPath,
		]);
	});
});
