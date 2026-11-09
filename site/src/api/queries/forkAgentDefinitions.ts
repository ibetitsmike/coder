// This file is fork code: queries for the fork-agent-definitions experiment.

import { queryOptions } from "react-query";
import { getUserAgentDefinitions } from "#/api/forkAgentDefinitions";

const userAgentDefinitionsKey = (organizationId: string) =>
	["chats", "fork-agent-definitions", "user", organizationId] as const;

/** Query options for the current user's agent definitions in an organization. */
export const userAgentDefinitions = (organizationId: string) =>
	queryOptions({
		queryKey: userAgentDefinitionsKey(organizationId),
		queryFn: () => getUserAgentDefinitions(organizationId),
	});
