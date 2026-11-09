// This file is fork code: the fork-nested-subagents experiment's subagent
// limits section, rendered after the upstream organization agent settings.

import { useContext } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import {
	organizationSubagentLimits,
	updateOrganizationSubagentLimits,
} from "#/api/queries/forkSubagentLimits";
import { organizationsPermissions } from "#/api/queries/organizations";
import type { Organization } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { ForkSubagentLimitsSettings } from "./components/ForkSubagentLimitsSettings";

type ForkSubagentLimitsProps = {
	organization: Organization;
	canEdit: boolean;
};

export const ForkSubagentLimits: React.FC<ForkSubagentLimitsProps> = ({
	organization,
	canEdit,
}) => {
	// Upstream stories and tests mount OrganizationAgentSettings without a
	// DashboardProvider, so a missing dashboard counts as the experiment off
	// instead of throwing like useDashboard.
	const experiments = useContext(DashboardContext)?.experiments ?? [];
	const queryClient = useQueryClient();
	const isExperimentEnabled = experiments.includes("fork-nested-subagents");
	const permissionsQuery = useQuery({
		...organizationsPermissions([organization.id]),
		enabled: isExperimentEnabled,
	});
	// The API returns 404 to members who cannot read the organization's chat
	// model configs, so the section is hidden for them.
	const canView =
		permissionsQuery.data?.[organization.id]?.viewChatModelConfigs ?? false;
	const isVisible = isExperimentEnabled && canView;
	const limitsQuery = useQuery({
		...organizationSubagentLimits(organization.id),
		enabled: isVisible,
	});
	const saveMutation = useMutation(
		updateOrganizationSubagentLimits(queryClient, organization.id),
	);

	if (!isVisible) {
		return null;
	}
	return (
		<div className="rounded-lg border border-solid border-border px-6 py-7">
			<ForkSubagentLimitsSettings
				limits={limitsQuery.data}
				isLoading={limitsQuery.isLoading}
				loadError={limitsQuery.error}
				canEdit={canEdit}
				onSave={saveMutation.mutate}
				isSaving={saveMutation.isPending}
				saveError={saveMutation.error}
			/>
		</div>
	);
};
