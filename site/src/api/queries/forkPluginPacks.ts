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
import { isResponseLost } from "./forkAgentDefinitions";

/** The query key of the plugin packs installed in an organization. */
export const pluginPacksKey = (organizationId: string) =>
	["chats", "fork-plugin-packs", organizationId] as const;

/** Query options for the plugin packs installed in an organization. */
export const pluginPacks = (organizationId: string) =>
	queryOptions({
		queryKey: pluginPacksKey(organizationId),
		queryFn: () => getPluginPacks(organizationId),
	});

const invalidatePluginPacks = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	queryClient.invalidateQueries({ queryKey: pluginPacksKey(organizationId) });

/**
 * Invalidates the pack list after a create or update. A lost response leaves
 * the outcome unknown, so the mutation then stays pending until the refetch
 * settles and callers can reconcile against cachedPluginPacks.
 */
const settlePluginPackChange = (
	queryClient: QueryClient,
	organizationId: string,
	error: unknown,
) => {
	const refetch = invalidatePluginPacks(queryClient, organizationId);
	return isResponseLost(error) ? refetch : undefined;
};

/** The organization's cached pack list, if it has loaded. */
export const cachedPluginPacks = (
	queryClient: QueryClient,
	organizationId: string,
) => queryClient.getQueryData(pluginPacks(organizationId).queryKey);

/** Whether an organization's cached pack list no longer lists the pack. */
export const isPluginPackGone = (
	queryClient: QueryClient,
	organizationId: string,
	packId: string,
) => {
	const packs = cachedPluginPacks(queryClient, organizationId);
	return packs !== undefined && !packs.some((pack) => pack.id === packId);
};

/** The list with the pack replacing any copy of it, in name order. */
const withPluginPack = (
	packs: readonly ForkPluginPack[],
	pack: ForkPluginPack,
) =>
	packs
		.filter((cached) => cached.id !== pack.id)
		.concat(pack)
		.sort((a, b) => a.name.localeCompare(b.name));

/**
 * Installs a pack. On success the returned pack goes into the cached list,
 * so a failed refetch cannot hide it. The list is then invalidated, and
 * after a lost response the mutation settles only once the refetch does.
 */
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
			settlePluginPackChange(queryClient, organizationId, error),
	});

/**
 * Moves a pack to the latest commit of its ref. The cache and refetch work
 * as in createPluginPackMutation.
 */
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
			settlePluginPackChange(queryClient, organizationId, error),
	});

/**
 * Deletes a pack. On success the pack leaves the cached list. After a 404 or
 * a lost response the mutation settles only once the refetch does, so
 * callers can check isPluginPackGone.
 */
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
		onSettled: (_data, error) => {
			const refetch = invalidatePluginPacks(queryClient, organizationId);
			const mayBeGone =
				isResponseLost(error) ||
				(isAxiosError(error) && error.response?.status === 404);
			return mayBeGone ? refetch : undefined;
		},
	});
