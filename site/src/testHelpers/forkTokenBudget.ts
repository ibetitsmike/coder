// This file is fork code: fixtures for the fork-token-budget experiment's
// organization token budget setting.

import type { ForkOrganizationTokenBudget } from "#/api/typesGenerated";

/** The setting of an organization that never changed it. */
export const MockForkOrganizationTokenBudget: ForkOrganizationTokenBudget = {
	strategy: "summary",
	fallback: "new_window",
};
