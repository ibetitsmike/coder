// This file is fork code: the fork-plugin-packs experiment's organization
// plugin packs page in AI settings.

import { useQuery } from "react-query";
import { Navigate } from "react-router";
import { pluginPacks } from "#/api/queries/forkPluginPacks";
import { organizationsPermissions } from "#/api/queries/organizations";
import type { Organization } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Loader } from "#/components/Loader/Loader";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { RequirePermission } from "#/modules/permissions/RequirePermission";
import { pageTitle } from "#/utils/page";
import { OrganizationPicker } from "../MCPServersPage/components/OrganizationPicker";
import { useRequestedSkillsOrganization } from "../SkillsPage/forkRequestedSkillsOrganization";
import { readableSkillsOrganizations } from "../SkillsPage/readableSkillsOrganizations";
import { ForkPluginPacksTableView } from "./ForkPluginPacksTableView";

type OrganizationPluginPacksProps = {
	organization: Organization;
	picker: React.ReactNode;
};

const OrganizationPluginPacks: React.FC<OrganizationPluginPacksProps> = ({
	organization,
	picker,
}) => {
	const packsQuery = useQuery(pluginPacks(organization.id));
	return (
		<ForkPluginPacksTableView
			packs={packsQuery.data ?? []}
			action={picker}
			error={packsQuery.error}
			isLoadingError={packsQuery.isLoadingError}
			isLoading={packsQuery.isLoading}
			isRetrying={packsQuery.isFetching}
			onRetry={() => void packsQuery.refetch()}
		/>
	);
};

const ForkPluginPacksPage: React.FC = () => {
	const { permissions } = useAuthenticated();
	const { experiments, organizations } = useDashboard();
	const isEnabled = experiments.includes("fork-plugin-packs");
	const organizationPermissionsQuery = useQuery({
		...organizationsPermissions(
			organizations.map((organization) => organization.id),
		),
		enabled: isEnabled && permissions.viewAnyOrganizationSkills,
	});

	const readableOrganizations = readableSkillsOrganizations(
		organizations,
		organizationPermissionsQuery.data,
	);
	const { organization, setOrganization } = useRequestedSkillsOrganization(
		readableOrganizations,
	);

	if (!isEnabled) {
		return <Navigate to="/ai/settings" replace />;
	}

	let content: React.ReactNode;
	if (organizationPermissionsQuery.isLoadingError) {
		content = <ErrorAlert error={organizationPermissionsQuery.error} />;
	} else if (!organizationPermissionsQuery.data) {
		content = <Loader />;
	} else {
		content = (
			<RequirePermission isFeatureVisible={Boolean(organization)}>
				{organizationPermissionsQuery.isRefetchError && (
					<ErrorAlert
						className="mb-4"
						error={organizationPermissionsQuery.error}
					/>
				)}
				{organization && (
					<OrganizationPluginPacks
						organization={organization}
						picker={
							<OrganizationPicker
								id="plugin-packs-organization"
								className="w-full sm:w-60"
								organizations={readableOrganizations}
								organization={organization}
								onChange={setOrganization}
								showLabel={false}
							/>
						}
					/>
				)}
			</RequirePermission>
		);
	}

	return (
		<RequirePermission isFeatureVisible={permissions.viewAnyOrganizationSkills}>
			<title>{pageTitle("Plugin packs", "AI Settings")}</title>
			{content}
		</RequirePermission>
	);
};

export default ForkPluginPacksPage;
