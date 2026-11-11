// This file is fork code: the fork-plugin-packs experiment's API calls live
// here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { ForkPluginPack } from "./typesGenerated";

const pluginPacksPath = (organizationId: string) =>
	`/api/v2/chats/fork/organizations/${organizationId}/plugin-packs`;

/** Lists the plugin packs installed in an organization. */
export const getPluginPacks = async (
	organizationId: string,
): Promise<ForkPluginPack[]> => {
	const response = await API.getAxiosInstance().get<ForkPluginPack[]>(
		pluginPacksPath(organizationId),
	);
	return response.data;
};
