// This file is fork code: query definitions for the fork-token-budget
// experiment's organization token budget setting.

import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getOrganizationTokenBudget,
	updateOrganizationTokenBudget as putOrganizationTokenBudget,
} from "#/api/forkTokenBudget";
import type { ForkOrganizationTokenBudget } from "#/api/typesGenerated";

const organizationTokenBudgetKey = (organizationId: string) =>
	["chats", "fork-token-budget", organizationId] as const;

/** Query options for an organization's token budget setting. */
export const organizationTokenBudget = (organizationId: string) =>
	queryOptions({
		queryKey: organizationTokenBudgetKey(organizationId),
		queryFn: ({ signal }) => getOrganizationTokenBudget(organizationId, signal),
	});

/** Mutation options that replace an organization's token budget setting. */
export const updateOrganizationTokenBudget = (
	queryClient: QueryClient,
	organizationId: string,
) =>
	mutationOptions({
		mutationFn: (req: ForkOrganizationTokenBudget) =>
			putOrganizationTokenBudget(organizationId, req),
		onSuccess: (setting) => {
			queryClient.setQueryData(
				organizationTokenBudgetKey(organizationId),
				setting,
			);
		},
	});
