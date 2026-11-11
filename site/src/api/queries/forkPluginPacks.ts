// This file is fork code: queries for the fork-plugin-packs experiment.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	createPluginPack,
	deletePluginPack,
	getPluginPacks,
	updatePluginPack,
} from "#/api/forkPluginPacks";
import type { CreateForkPluginPackRequest } from "#/api/typesGenerated";

const pluginPacksKey = (organizationId: string) =>
	["chats", "fork-plugin-packs", organizationId] as const;

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
		mutationFn: (packId: string) =>
			updatePluginPack(organizationId, packId, {}),
		onSuccess: () => invalidatePluginPacks(queryClient, organizationId),
	});

export const deletePluginPackMutation = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (packId: string) => deletePluginPack(organizationId, packId),
		onSuccess: () => invalidatePluginPacks(queryClient, organizationId),
	});
