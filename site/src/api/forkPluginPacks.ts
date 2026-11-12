// This file is fork code: the fork-plugin-packs experiment's API calls live
// here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type {
	CreateForkPluginPackRequest,
	ForkPluginPack,
	UpdateForkPluginPackRequest,
} from "./typesGenerated";

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

/** Fetches a plugin pack from Git and installs it pinned to one commit. */
export const createPluginPack = async (
	organizationId: string,
	req: CreateForkPluginPackRequest,
): Promise<ForkPluginPack> => {
	const response = await API.getAxiosInstance().post<ForkPluginPack>(
		pluginPacksPath(organizationId),
		req,
	);
	return response.data;
};

/** Fetches a plugin pack again and pins it to the commit its ref names now. */
export const updatePluginPack = async (
	organizationId: string,
	packId: string,
	req: UpdateForkPluginPackRequest,
): Promise<ForkPluginPack> => {
	const response = await API.getAxiosInstance().post<ForkPluginPack>(
		`${pluginPacksPath(organizationId)}/${packId}/update`,
		req,
	);
	return response.data;
};

/** Deletes a plugin pack and its files. */
export const deletePluginPack = async (
	organizationId: string,
	packId: string,
): Promise<void> => {
	await API.getAxiosInstance().delete(
		`${pluginPacksPath(organizationId)}/${packId}`,
	);
};
