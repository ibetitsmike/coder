// This file is fork code: the fork sections rendered after the upstream
// organization agent settings, so OrganizationAgentSettings.tsx keeps one
// hook line.

import type { Organization } from "#/api/typesGenerated";
import { ForkSubagentLimits } from "./ForkSubagentLimits";
import { ForkTokenBudget } from "./ForkTokenBudget";

type ForkOrganizationAgentSettingsProps = {
	organization: Organization;
	canEdit: boolean;
};

/**
 * Add a fork section here, not in OrganizationAgentSettings.tsx, so the
 * upstream file keeps its single hook line through syncs.
 */
export const ForkOrganizationAgentSettings: React.FC<
	ForkOrganizationAgentSettingsProps
> = ({ organization, canEdit }) => (
	<>
		<ForkSubagentLimits organization={organization} canEdit={canEdit} />
		<ForkTokenBudget organization={organization} canEdit={canEdit} />
	</>
);
