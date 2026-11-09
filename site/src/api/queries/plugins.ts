import type { QueryClient } from "react-query";
import { API } from "#/api/api";

export const linkablePluginsKey = ["linkable-plugins"] as const;

export const linkablePlugins = () => ({
	queryKey: linkablePluginsKey,
	queryFn: () => API.experimental.getLinkablePlugins(),
	// Links are made outside Coder, by sending a code to the bot, so refetch
	// when the user returns to the tab to show the new link.
	refetchOnWindowFocus: true,
});

export const createPluginLinkCode = (queryClient: QueryClient) => ({
	mutationFn: (pluginId: string) =>
		API.experimental.createPluginLinkCode(pluginId),
	// The page compares later links against the current list to tell when the
	// code is used, and times expiry from the last fetch, so both must be fresh.
	onSuccess: () =>
		queryClient.invalidateQueries({ queryKey: linkablePluginsKey }),
});

type DeletePluginLinkArgs = {
	pluginId: string;
	externalId: string;
};

export const deletePluginLink = (queryClient: QueryClient) => ({
	mutationFn: ({ pluginId, externalId }: DeletePluginLinkArgs) =>
		API.experimental.deletePluginLink(pluginId, externalId),
	onSettled: () =>
		queryClient.invalidateQueries({ queryKey: linkablePluginsKey }),
});
