import type { QueryClient } from "react-query";
import { API } from "#/api/api";
import type { LinkablePlugin } from "#/api/typesGenerated";

export const linkablePluginsKey = ["linkable-plugins"] as const;

export const linkablePlugins = () => ({
	queryKey: linkablePluginsKey,
	queryFn: () => API.experimental.getLinkablePlugins(),
	// Links are made outside Coder, by sending a code to the plugin, so
	// refetch when the user returns to the tab to show the new link.
	refetchOnWindowFocus: true,
});

/**
 * startPluginLink creates a link code and, for a plugin that connects
 * accounts, gets its connect prompt.
 */
export const startPluginLink = (queryClient: QueryClient) => ({
	mutationFn: async (plugin: LinkablePlugin) => {
		// The prompt comes first, so a plugin that can't make one leaves no
		// unused code behind.
		const connectPrompt = plugin.connect
			? await API.experimental.connectPlugin(plugin.id)
			: undefined;
		const linkCode = await API.experimental.createPluginLinkCode(plugin.id);
		return { linkCode, connectPrompt };
	},
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
