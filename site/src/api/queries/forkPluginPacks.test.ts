import { AxiosError } from "axios";
import { MutationObserver, QueryClient } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkPluginPacksApi from "#/api/forkPluginPacks";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import {
	createPluginPackMutation,
	pluginPacks,
	pluginPacksKey,
	reconcileInstalledPluginPack,
	reconcileUpdatedPluginPack,
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

	it("keeps a cached pack that is newer than the response", async () => {
		const queryClient = createQueryClient();
		const mockNewerListedPack = {
			...MockForkPluginPack,
			updated_at: "2026-11-12T12:00:00Z",
		};
		queryClient.setQueryData(listKey, [mockNewerListedPack]);
		vi.spyOn(forkPluginPacksApi, "createPluginPack").mockResolvedValue(
			MockForkPluginPack,
		);

		await new MutationObserver(
			queryClient,
			createPluginPackMutation(queryClient, organizationId),
		).mutate(req);

		expect(queryClient.getQueryData(listKey)).toEqual([mockNewerListedPack]);
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

	it("keeps a cached pack that is newer than the response", async () => {
		const queryClient = createQueryClient();
		const mockNewerListedPack = {
			...MockForkPluginPack,
			updated_at: "2026-11-12T12:00:00Z",
		};
		queryClient.setQueryData(listKey, [mockNewerListedPack]);
		vi.spyOn(forkPluginPacksApi, "updatePluginPack").mockResolvedValue(
			mockNewerPack,
		);

		await new MutationObserver(
			queryClient,
			updatePluginPackMutation(queryClient, organizationId),
		).mutate(MockForkPluginPack);

		expect(queryClient.getQueryData(listKey)).toEqual([mockNewerListedPack]);
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
	it("keeps the server's order and leaves the detail-only files out", async () => {
		const queryClient = createQueryClient();
		// The server's collation may order names differently from the browser.
		queryClient.setQueryData(listKey, [
			MockForkPluginPackWithoutPath,
			MockForkPluginPack,
		]);
		vi.spyOn(forkPluginPacksApi, "updatePluginPack").mockResolvedValue({
			...mockNewerPack,
			files: [
				{ path: "plugin.json", size_bytes: 2, content_hash: "sha256:00" },
			],
		});

		await new MutationObserver(
			queryClient,
			updatePluginPackMutation(queryClient, organizationId),
		).mutate(MockForkPluginPack);

		expect(queryClient.getQueryData(listKey)).toEqual([
			MockForkPluginPackWithoutPath,
			mockNewerPack,
		]);
	});

	it("refetches the list after a lost response even without observers", async () => {
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false } },
		});
		const getPacks = vi
			.spyOn(forkPluginPacksApi, "getPluginPacks")
			.mockResolvedValue([MockForkPluginPack]);
		await queryClient.prefetchQuery(pluginPacks(organizationId));
		vi.spyOn(forkPluginPacksApi, "updatePluginPack").mockRejectedValue(
			new AxiosError("Network Error"),
		);

		await new MutationObserver(
			queryClient,
			updatePluginPackMutation(queryClient, organizationId),
		)
			.mutate(MockForkPluginPack)
			.catch(() => undefined);

		expect(getPacks).toHaveBeenCalledTimes(2);
	});
});

describe("reconcileInstalledPluginPack", () => {
	const lostResponse = new AxiosError("Network Error", "ERR_NETWORK");
	const req = {
		name: MockForkPluginPack.name,
		source_url: MockForkPluginPack.source_url,
		source_path: MockForkPluginPack.source_path,
		ref: MockForkPluginPack.ref,
	};

	it("finds an unfamiliar pack with the submitted source", () => {
		const queryClient = createQueryClient();
		queryClient.setQueryData(listKey, [MockForkPluginPack]);

		expect(
			reconcileInstalledPluginPack(
				queryClient,
				organizationId,
				req,
				[],
				lostResponse,
			),
		).toEqual(MockForkPluginPack);
	});

	it.each([
		["source URL", { source_url: "https://git.example.com/other.git" }],
		["path", { source_path: "other" }],
		["ref", { ref: "main" }],
	])("ignores a same-name pack with another %s", (_field, change) => {
		const queryClient = createQueryClient();
		queryClient.setQueryData(listKey, [{ ...MockForkPluginPack, ...change }]);

		expect(
			reconcileInstalledPluginPack(
				queryClient,
				organizationId,
				req,
				[],
				lostResponse,
			),
		).toBeUndefined();
	});
});

describe("reconcileUpdatedPluginPack", () => {
	const lostResponse = new AxiosError("Network Error", "ERR_NETWORK");
	const mockRefreshedPack = {
		...MockForkPluginPack,
		updated_at: "2026-11-12T12:00:00Z",
	};

	it("finds a refreshed pack that keeps the ref", () => {
		const queryClient = createQueryClient();
		queryClient.setQueryData(listKey, [mockRefreshedPack]);

		expect(
			reconcileUpdatedPluginPack(
				queryClient,
				organizationId,
				MockForkPluginPack,
				lostResponse,
			),
		).toEqual(mockRefreshedPack);
	});

	it("ignores a refreshed pack that has another ref", () => {
		const queryClient = createQueryClient();
		queryClient.setQueryData(listKey, [{ ...mockRefreshedPack, ref: "main" }]);

		expect(
			reconcileUpdatedPluginPack(
				queryClient,
				organizationId,
				MockForkPluginPack,
				lostResponse,
			),
		).toBeUndefined();
	});
});
