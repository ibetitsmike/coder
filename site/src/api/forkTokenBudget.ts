// This file is fork code: the fork-token-budget experiment's organization
// token budget calls live here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { ForkOrganizationTokenBudget } from "./typesGenerated";

/** Fetches the organization's token budget setting. */
export const getOrganizationTokenBudget = async (
	organizationId: string,
	signal?: AbortSignal,
): Promise<ForkOrganizationTokenBudget> => {
	const response =
		await API.getAxiosInstance().get<ForkOrganizationTokenBudget>(
			`/api/v2/chats/fork/organizations/${organizationId}/token-budget`,
			{ signal },
		);
	return response.data;
};

/** Replaces the organization's token budget setting. */
export const updateOrganizationTokenBudget = async (
	organizationId: string,
	req: ForkOrganizationTokenBudget,
): Promise<ForkOrganizationTokenBudget> => {
	const response =
		await API.getAxiosInstance().put<ForkOrganizationTokenBudget>(
			`/api/v2/chats/fork/organizations/${organizationId}/token-budget`,
			req,
		);
	return response.data;
};
