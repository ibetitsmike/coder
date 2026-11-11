// This file is fork code: queries for the fork-plugin-packs experiment.

import { queryOptions } from "react-query";
import { getPluginPacks } from "#/api/forkPluginPacks";

const pluginPacksKey = (organizationId: string) =>
	["chats", "fork-plugin-packs", organizationId] as const;

/** Query options for the plugin packs installed in an organization. */
export const pluginPacks = (organizationId: string) =>
	queryOptions({
		queryKey: pluginPacksKey(organizationId),
		queryFn: () => getPluginPacks(organizationId),
	});
