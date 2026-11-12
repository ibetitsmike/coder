// This file is fork code: the fork-plugin-packs experiment's enablement API.

import { API } from "./api";
import type { ForkEnabledPluginPack } from "./typesGenerated";

/**
 * The scope whose direct plugin pack enablements are listed or changed. A chat
 * target never covers the packs the chat inherits from its project.
 */
export type PluginPackTarget =
	| { kind: "project"; organizationId: string; projectId: string }
	| { kind: "chat"; chatId: string };

const enabledPluginPacksPath = (target: PluginPackTarget) =>
	target.kind === "project"
		? `/api/v2/chats/fork/organizations/${target.organizationId}/projects/${target.projectId}/plugin-packs`
		: `/api/v2/chats/fork/${target.chatId}/plugin-packs`;

/** Lists the plugin packs any organization member may enable. */
export const getAvailablePluginPacks = async (
	organizationId: string,
): Promise<ForkEnabledPluginPack[]> => {
	const response = await API.getAxiosInstance().get<ForkEnabledPluginPack[]>(
		`/api/v2/chats/fork/organizations/${organizationId}/available-plugin-packs`,
	);
	return response.data;
};

/**
 * Lists the plugin packs enabled for the target itself. A chat's list leaves
 * out the packs its project enables.
 */
export const getEnabledPluginPacks = async (
	target: PluginPackTarget,
): Promise<ForkEnabledPluginPack[]> => {
	const response = await API.getAxiosInstance().get<ForkEnabledPluginPack[]>(
		enabledPluginPacksPath(target),
	);
	return response.data;
};

/** Enables or disables one plugin pack for the target. */
export const setPluginPackEnabled = async (
	target: PluginPackTarget,
	packId: string,
	enabled: boolean,
): Promise<void> => {
	const path = `${enabledPluginPacksPath(target)}/${packId}`;
	if (enabled) {
		await API.getAxiosInstance().put(path);
	} else {
		await API.getAxiosInstance().delete(path);
	}
};
