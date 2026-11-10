// This file is fork code: the fork-nested-subagents experiment's organization
// subagent limit calls live here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type {
	ForkOrganizationSubagentLimits,
	ForkOrganizationSubagentLimitsResponse,
} from "./typesGenerated";

/** Fetches the organization's subagent limits with the deployment limits. */
export const getOrganizationSubagentLimits = async (
	organizationId: string,
	signal?: AbortSignal,
): Promise<ForkOrganizationSubagentLimitsResponse> => {
	const response =
		await API.getAxiosInstance().get<ForkOrganizationSubagentLimitsResponse>(
			`/api/v2/chats/fork/organizations/${organizationId}/subagent-limits`,
			{ signal },
		);
	return response.data;
};

/**
 * Replaces the organization's subagent limits. An omitted limit is unset and
 * uses the deployment value.
 */
export const updateOrganizationSubagentLimits = async (
	organizationId: string,
	req: ForkOrganizationSubagentLimits,
): Promise<ForkOrganizationSubagentLimitsResponse> => {
	const response =
		await API.getAxiosInstance().put<ForkOrganizationSubagentLimitsResponse>(
			`/api/v2/chats/fork/organizations/${organizationId}/subagent-limits`,
			req,
		);
	return response.data;
};
