// This file is fork code: queries for the fork-plugin-packs experiment.

import { isAxiosError } from "axios";
import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	createPluginPack,
	deletePluginPack,
	getPluginPacks,
	updatePluginPack,
} from "#/api/forkPluginPacks";
import type {
	CreateForkPluginPackRequest,
	ForkPluginPack,
} from "#/api/typesGenerated";
import {
	invalidateList,
	isNewer,
	isResponseLost,
} from "./forkAgentDefinitions";

/** The key prefix shared by every plugin pack query. */
export const pluginPacksRootKey = ["chats", "fork-plugin-packs"] as const;

/**
 * The query key of an organization's plugin pack list. It also prefixes the
 * keys of the organization's projects' enabled pack lists.
 */
export const pluginPacksKey = (organizationId: string) =>
	[...pluginPacksRootKey, organizationId] as const;

/** Query options for the plugin packs installed in an organization. */
export const pluginPacks = (organizationId: string) =>
	queryOptions({
		queryKey: pluginPacksKey(organizationId),
		queryFn: () => getPluginPacks(organizationId),
	});

const invalidatePluginPacks = (
	queryClient: QueryClient,
	organizationId: string,
	awaitRefetch: boolean,
) =>
	invalidateList(queryClient, pluginPacksKey(organizationId), {
		exact: false,
		awaitRefetch,
	});

/** The organization's cached pack list, or undefined when it is not loaded. */
export const cachedPluginPacks = (
	queryClient: QueryClient,
	organizationId: string,
) => queryClient.getQueryData(pluginPacks(organizationId).queryKey);

/**
 * Whether the cached pack list no longer lists the pack. False when the list
 * is not loaded.
 */
export const isPluginPackGone = (
	queryClient: QueryClient,
	organizationId: string,
	packId: string,
) => {
	const packs = cachedPluginPacks(queryClient, organizationId);
	return packs !== undefined && !packs.some((pack) => pack.id === packId);
};

/**
 * After a lost response, a refetched copy of the pack with a new timestamp
 * and the ref the update started from shows that the update went through.
 */
export const reconcileUpdatedPluginPack = (
	queryClient: QueryClient,
	organizationId: string,
	pack: ForkPluginPack,
	error: unknown,
) => {
	if (!isResponseLost(error)) {
		return undefined;
	}

	return cachedPluginPacks(queryClient, organizationId)?.find(
		(candidate) =>
			candidate.id === pack.id &&
			candidate.ref === pack.ref &&
			candidate.updated_at !== pack.updated_at,
	);
};

/**
 * After a lost response, a refetched pack with the submitted name and source
 * that was not listed before the request shows that the install went through.
 * Without that earlier list, a pack with the name may predate the request.
 */
export const reconcileInstalledPluginPack = (
	queryClient: QueryClient,
	organizationId: string,
	req: CreateForkPluginPackRequest,
	knownPackIds: readonly string[] | undefined,
	error: unknown,
) => {
	if (!knownPackIds || !isResponseLost(error)) {
		return undefined;
	}

	return cachedPluginPacks(queryClient, organizationId)?.find(
		(candidate) =>
			candidate.name === req.name &&
			candidate.source_url === req.source_url &&
			candidate.source_path === (req.source_path ?? "") &&
			candidate.ref === req.ref &&
			!knownPackIds.includes(candidate.id),
	);
};

const withPluginPack = (
	packs: readonly ForkPluginPack[],
	pack: ForkPluginPack,
) => {
	const replaced = packs.filter(
		(cached) => cached.id === pack.id || cached.name === pack.name,
	);
	if (replaced.some((cached) => isNewer(cached, pack))) {
		return packs;
	}

	return packs
		.filter((cached) => !replaced.includes(cached))
		.concat(pack)
		.sort((a, b) => a.name.localeCompare(b.name));
};

/** Installs a pack and caches it, so a failed refetch cannot hide it. */
export const createPluginPackMutation = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (req: CreateForkPluginPackRequest) =>
			createPluginPack(organizationId, req),
		// An unloaded list still gets the pack, so a failed first load cannot
		// hide it. A refetch replaces the one-pack list with the full list.
		onSuccess: (pack) => {
			queryClient.setQueryData(pluginPacks(organizationId).queryKey, (packs) =>
				withPluginPack(packs ?? [], pack),
			);
		},
		onSettled: (_data, error) =>
			invalidatePluginPacks(queryClient, organizationId, isResponseLost(error)),
	});

/** Moves a pack to the latest commit of its ref and caches the result. */
export const updatePluginPackMutation = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (pack: ForkPluginPack) =>
			updatePluginPack(organizationId, pack.id, {}),
		// An unloaded list stays unloaded, because one pack is not a list.
		onSuccess: (updated) => {
			queryClient.setQueryData(
				pluginPacks(organizationId).queryKey,
				(packs) => packs && withPluginPack(packs, updated),
			);
		},
		onSettled: (_data, error) =>
			invalidatePluginPacks(queryClient, organizationId, isResponseLost(error)),
	});

/** Deletes a pack and removes it from the cached list. */
export const deletePluginPackMutation = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (packId: string) => deletePluginPack(organizationId, packId),
		onSuccess: (_data, packId) => {
			queryClient.setQueryData(pluginPacks(organizationId).queryKey, (packs) =>
				packs?.filter((pack) => pack.id !== packId),
			);
		},
		onSettled: (_data, error) =>
			invalidatePluginPacks(
				queryClient,
				organizationId,
				isResponseLost(error) ||
					(isAxiosError(error) && error.response?.status === 404),
			),
	});
