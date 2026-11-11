// This file is fork code: queries for enabling plugin packs in the
// fork-plugin-packs experiment.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getEnabledPluginPacks,
	type PluginPackTarget,
	setPluginPackEnabled,
} from "#/api/forkPluginPackEnablement";

const enabledPluginPacksKey = (target: PluginPackTarget) =>
	target.kind === "project"
		? ([
				"chats",
				"fork-plugin-packs",
				target.organizationId,
				"projects",
				target.projectId,
			] as const)
		: (["chats", "fork-plugin-packs", "chats", target.chatId] as const);

export const enabledPluginPacks = (target: PluginPackTarget) =>
	queryOptions({
		queryKey: enabledPluginPacksKey(target),
		queryFn: () => getEnabledPluginPacks(target),
	});

/** A pack's requested enabled state for a target. */
export type PluginPackToggle = { packId: string; enabled: boolean };

export const setPluginPackEnabledMutation = (
	queryClient: QueryClient,
	target: PluginPackTarget,
) =>
	mutationOptions({
		mutationFn: ({ packId, enabled }: PluginPackToggle) =>
			setPluginPackEnabled(target, packId, enabled),
		onSettled: () =>
			queryClient.invalidateQueries({
				queryKey: enabledPluginPacksKey(target),
			}),
	});
