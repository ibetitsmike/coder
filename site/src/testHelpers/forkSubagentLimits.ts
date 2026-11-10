// This file is fork code: fixtures for the fork-nested-subagents experiment's
// organization subagent limits.

import type { ForkOrganizationSubagentLimitsResponse } from "#/api/typesGenerated";

/** An organization that lowers the depth and sets an organization-wide cap. */
export const MockForkOrganizationSubagentLimits: ForkOrganizationSubagentLimitsResponse =
	{
		organization: { max_depth: 2, max_concurrent: 10 },
		deployment: {
			max_depth: 4,
			max_concurrent: 0,
			max_concurrent_per_family: 8,
			max_live_per_family: 0,
		},
		effective: {
			max_depth: 2,
			max_concurrent: 10,
			max_concurrent_per_family: 8,
			max_live_per_family: 0,
		},
	};
