// This file is fork code: the fork-token-budget experiment's token budget
// section, rendered after the upstream organization agent settings.

import { useContext } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import {
	organizationTokenBudget,
	updateOrganizationTokenBudget,
} from "#/api/queries/forkTokenBudget";
import { organizationsPermissions } from "#/api/queries/organizations";
import type { Organization } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { ForkTokenBudgetSettings } from "./components/ForkTokenBudgetSettings";

type ForkTokenBudgetProps = {
	organization: Organization;
	canEdit: boolean;
};

/**
 * Requests the setting only after the permissions check passes, because the
 * API answers members without chat model config access with a 404.
 */
export const ForkTokenBudget: React.FC<ForkTokenBudgetProps> = ({
	organization,
	canEdit,
}) => {
	// Upstream stories and tests mount OrganizationAgentSettings without a
	// DashboardProvider, so a missing dashboard counts as the experiment off
	// instead of throwing like useDashboard.
	const isExperimentEnabled =
		useContext(DashboardContext)?.experiments.includes("fork-token-budget") ??
		false;
	const queryClient = useQueryClient();
	const permissionsQuery = useQuery({
		...organizationsPermissions([organization.id]),
		enabled: isExperimentEnabled,
	});
	const canView =
		permissionsQuery.data?.[organization.id]?.viewChatModelConfigs ?? false;
	const isVisible = isExperimentEnabled && canView;
	const settingQuery = useQuery({
		...organizationTokenBudget(organization.id),
		enabled: isVisible,
	});
	const saveMutation = useMutation(
		updateOrganizationTokenBudget(queryClient, organization.id),
	);

	if (!isVisible) {
		return null;
	}
	return (
		<div className="rounded-lg border border-solid border-border px-6 py-7">
			<ForkTokenBudgetSettings
				setting={settingQuery.data}
				isLoading={settingQuery.isLoading}
				loadError={settingQuery.error}
				canEdit={canEdit}
				onSave={saveMutation.mutate}
				isSaving={saveMutation.isPending}
				saveError={saveMutation.error}
			/>
		</div>
	);
};
