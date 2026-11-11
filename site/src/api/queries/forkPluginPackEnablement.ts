// This file is fork code: queries for enabling plugin packs in the
// fork-plugin-packs experiment.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getEnabledPluginPacks,
	type PluginPackTarget,
	setPluginPackEnabled,
} from "#/api/forkPluginPackEnablement";
import type { ForkEnabledPluginPack } from "#/api/typesGenerated";
import { chatEntityKey } from "./chats";
import { pluginPacksKey, pluginPacksRootKey } from "./forkPluginPacks";

/**
 * The query key of the plugin packs a project or chat enables directly. A
 * project's key extends pluginPacksKey of its organization.
 */
export const enabledPluginPacksKey = (target: PluginPackTarget) =>
	target.kind === "project"
		? ([
				...pluginPacksKey(target.organizationId),
				"projects",
				target.projectId,
			] as const)
		: ([...pluginPacksRootKey, "chats", target.chatId] as const);

export const enabledPluginPacks = (target: PluginPackTarget) =>
	queryOptions({
		queryKey: enabledPluginPacksKey(target),
		queryFn: () => getEnabledPluginPacks(target),
	});

export type PluginPackToggle = {
	pack: ForkEnabledPluginPack;
	enabled: boolean;
};

/**
 * Writes the new state into the cached enabled list on success, so a failed
 * refetch cannot revert the switch. For a chat target the chat is also
 * refetched.
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
		onSettled: async () => {
			await Promise.all([
				queryClient.invalidateQueries({
					queryKey: enabledPluginPacksKey(target),
				}),
				// The chat's context lists only resources from enabled packs, and
				// waiting chats are not polled, so refetch it for the slash menu.
				target.kind === "chat" &&
					queryClient.invalidateQueries({
						queryKey: chatEntityKey(target.chatId),
						exact: true,
					}),
			]);
		},
	});
