// This file is fork code: query definitions for the fork-nested-subagents
// experiment's organization subagent limits.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getOrganizationSubagentLimits,
	updateOrganizationSubagentLimits as putOrganizationSubagentLimits,
} from "#/api/forkSubagentLimits";
import type { ForkOrganizationSubagentLimits } from "#/api/typesGenerated";

const organizationSubagentLimitsKey = (organizationId: string) =>
	["chats", "fork-subagent-limits", organizationId] as const;

export const organizationSubagentLimits = (organizationId: string) =>
	queryOptions({
		queryKey: organizationSubagentLimitsKey(organizationId),
		queryFn: ({ signal }) =>
			getOrganizationSubagentLimits(organizationId, signal),
	});

export const updateOrganizationSubagentLimits = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (req: ForkOrganizationSubagentLimits) =>
			putOrganizationSubagentLimits(organizationId, req),
		onSuccess: (limits) => {
			queryClient.setQueryData(
				organizationSubagentLimitsKey(organizationId),
				limits,
			);
		},
	});
