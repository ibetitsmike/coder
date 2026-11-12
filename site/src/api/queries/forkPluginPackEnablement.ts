// This file is fork code: queries for enabling plugin packs in the
// fork-plugin-packs experiment.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import { getErrorStatus } from "#/api/errors";
import {
	getAvailablePluginPacks,
	getEnabledPluginPacks,
	getExcludedPluginPacks,
	type PluginPackTarget,
	setPluginPackEnabled,
	setPluginPackExcluded,
} from "#/api/forkPluginPackEnablement";
import type { ForkEnabledPluginPack } from "#/api/typesGenerated";
import { chatEntityKey } from "./chats";
import { pluginPacksKey, pluginPacksRootKey } from "./forkPluginPacks";

/**
 * The query key of the plugin packs any organization member may enable. It
 * extends pluginPacksKey, so installing, updating or deleting a pack
 * refreshes it.
 */
export const availablePluginPacksKey = (organizationId: string) =>
	[...pluginPacksKey(organizationId), "available"] as const;

/**
 * The query key of the plugin packs a project or chat enables directly. A
 * project's key extends pluginPacksKey of its organization, but a chat's key
 * does not.
 */
export const enabledPluginPacksKey = (target: PluginPackTarget) =>
	target.kind === "project"
		? ([
				...pluginPacksKey(target.organizationId),
				"projects",
				target.projectId,
			] as const)
		: ([...pluginPacksRootKey, "chats", target.chatId] as const);

/**
 * The query key of the plugin packs turned off for a chat. It extends the
 * chat's enabledPluginPacksKey, so invalidating that key refreshes it too.
 */
export const excludedPluginPacksKey = (chatId: string) =>
	[...enabledPluginPacksKey({ kind: "chat", chatId }), "excluded"] as const;

/** Query options for the plugin packs any organization member may enable. */
export const availablePluginPacks = (organizationId: string) =>
	queryOptions({
		queryKey: availablePluginPacksKey(organizationId),
		queryFn: () => getAvailablePluginPacks(organizationId),
	});

/** Query options for the plugin packs a project or chat enables directly. */
export const enabledPluginPacks = (target: PluginPackTarget) =>
	queryOptions({
		queryKey: enabledPluginPacksKey(target),
		queryFn: () => getEnabledPluginPacks(target),
	});

/** Query options for the plugin packs turned off for a chat. */
export const excludedPluginPacks = (chatId: string) =>
	queryOptions({
		queryKey: excludedPluginPacksKey(chatId),
		queryFn: () => getExcludedPluginPacks(chatId),
	});

/** Where a pack's switch gets its state for a project or chat. */
export type PluginPackState = {
	/** The target enables the pack itself. */
	direct: boolean;
	/** The chat's project enables the pack. Always false for a project. */
	inherited: boolean;
	/** The chat turns the pack off. Always false for a project. */
	excluded: boolean;
};

/**
 * A request to switch a plugin pack. `pack` is the snapshot used to reconcile
 * the cached lists, `enabled` is the desired state of the switch and `writes`
 * holds the rows to change; a missing row stays as it is.
 */
export type PluginPackToggle = {
	pack: ForkEnabledPluginPack;
	enabled: boolean;
	writes: { direct?: boolean; excluded?: boolean };
};

/**
 * Picks the row writes that turn a pack's switch on or off. A chat cannot
 * remove its project's row, so turning off an inherited pack excludes it.
 */
export const pluginPackToggle = (
	pack: ForkEnabledPluginPack,
	enabled: boolean,
	{ direct, inherited, excluded }: PluginPackState,
): PluginPackToggle => ({
	pack,
	enabled,
	writes: enabled
		? {
				excluded: excluded ? false : undefined,
				direct: direct || inherited ? undefined : true,
			}
		: inherited
			? { excluded: true }
			: { direct: false },
});

const withPack = (
	packs: ForkEnabledPluginPack[] | undefined,
	pack: ForkEnabledPluginPack,
	present: boolean,
) => {
	const others = packs?.filter((cached) => cached.id !== pack.id);
	return present && others
		? [
				...others,
				{ id: pack.id, name: pack.name, commit_sha: pack.commit_sha },
			]
		: others;
};

/**
 * Writes the new rows into the cached lists on success, so a failed refetch
 * cannot revert the switch. A 404 means the pack was deleted, so the
 * organization's pack lists are refetched too.
 */
export const setPluginPackEnabledMutation = (
	queryClient: QueryClient,
	target: PluginPackTarget,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: async ({ pack, writes }: PluginPackToggle) => {
			if (writes.excluded !== undefined && target.kind === "chat") {
				await setPluginPackExcluded(target.chatId, pack.id, writes.excluded);
			}
			if (writes.direct !== undefined) {
				await setPluginPackEnabled(target, pack.id, writes.direct);
			}
		},
		onSuccess: (_data, { pack, writes }) => {
			const { direct, excluded } = writes;
			if (direct !== undefined) {
				queryClient.setQueryData<ForkEnabledPluginPack[]>(
					enabledPluginPacksKey(target),
					(packs) => withPack(packs, pack, direct),
				);
			}
			if (excluded !== undefined && target.kind === "chat") {
				queryClient.setQueryData<ForkEnabledPluginPack[]>(
					excludedPluginPacksKey(target.chatId),
					(packs) => withPack(packs, pack, excluded),
				);
			}
		},
		onSettled: async (_data, error) => {
			await Promise.all([
				queryClient.invalidateQueries({
					queryKey: enabledPluginPacksKey(target),
				}),
				getErrorStatus(error) === 404 &&
					queryClient.invalidateQueries({
						queryKey: pluginPacksKey(organizationId),
					}),
				// The chat's context lists only resources from enabled packs, and
				// waiting chats are not polled, so refetch it for the slash menu.
				// A pack the workspace does not hold yet is written on the chat's
				// next turn, so its resources appear only after that turn.
				target.kind === "chat" &&
					queryClient.invalidateQueries({
						queryKey: chatEntityKey(target.chatId),
						exact: true,
					}),
			]);
		},
	});
