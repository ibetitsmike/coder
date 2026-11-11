// This file is fork code: queries for enabling plugin packs in the
// fork-plugin-packs experiment.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getEnabledPluginPacks,
	type PluginPackTarget,
	setPluginPackEnabled,
} from "#/api/forkPluginPackEnablement";
import type { ForkEnabledPluginPack } from "#/api/typesGenerated";

/** The query key of the plugin packs a project or chat enables directly. */
export const enabledPluginPacksKey = (target: PluginPackTarget) =>
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
export type PluginPackToggle = {
	pack: ForkEnabledPluginPack;
	enabled: boolean;
};

/**
 * Enables or disables a pack for a target. On success the cached enabled
 * list changes to match, so a failed refetch cannot revert the switch. The
 * list is then invalidated.
 */
export const setPluginPackEnabledMutation = (
	queryClient: QueryClient,
	target: PluginPackTarget,
) =>
	mutationOptions({
		mutationFn: ({ pack, enabled }: PluginPackToggle) =>
			setPluginPackEnabled(target, pack.id, enabled),
		onSuccess: (_data, { pack, enabled }) => {
			const queryKey = enabledPluginPacks(target).queryKey;
			queryClient.setQueryData(queryKey, (packs) => {
				const others = packs?.filter((cached) => cached.id !== pack.id);
				return enabled && others
					? [
							...others,
							{ id: pack.id, name: pack.name, commit_sha: pack.commit_sha },
						]
					: others;
			});
		},
		onSettled: () =>
			queryClient.invalidateQueries({
				queryKey: enabledPluginPacksKey(target),
			}),
	});
