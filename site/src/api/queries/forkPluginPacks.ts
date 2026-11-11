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

/** Whether an organization's cached pack list no longer lists the pack. */
export const isPluginPackGone = (
	queryClient: QueryClient,
	organizationId: string,
	packId: string,
) => {
	const packs = queryClient.getQueryData(pluginPacks(organizationId).queryKey);
	return packs !== undefined && !packs.some((pack) => pack.id === packId);
};

export const createPluginPackMutation = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (req: CreateForkPluginPackRequest) =>
			createPluginPack(organizationId, req),
		onSuccess: () => invalidatePluginPacks(queryClient, organizationId),
	});

export const updatePluginPackMutation = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (pack: ForkPluginPack) =>
			updatePluginPack(organizationId, pack.id, {}),
		onSuccess: () => invalidatePluginPacks(queryClient, organizationId),
	});

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
		// A 404 or a lost response can mean the pack is already gone, so the
		// mutation stays pending until the refetch settles and callers can
		// check the list with isPluginPackGone.
		onSettled: (_data, error) => {
			const refetch = invalidatePluginPacks(queryClient, organizationId);
			const mayBeGone =
				isResponseLost(error) ||
				(isAxiosError(error) && error.response?.status === 404);
			return mayBeGone ? refetch : undefined;
		},
	});
