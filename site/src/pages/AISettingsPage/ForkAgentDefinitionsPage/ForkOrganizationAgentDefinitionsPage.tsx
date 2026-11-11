// This file is fork code: the fork-agent-definitions experiment's
// organization agent definitions page in AI settings.

import { useState } from "react";
import { useQuery } from "react-query";
import { Navigate, useSearchParams } from "react-router";
import { organizationsPermissions } from "#/api/queries/organizations";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Loader } from "#/components/Loader/Loader";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { RequirePermission } from "#/modules/permissions/RequirePermission";
import { ForkAgentDefinitionsTable } from "#/pages/AgentsPage/AgentSettingsPage/ForkAgentDefinitionsPage/ForkAgentDefinitionsTable";
import { pageTitle } from "#/utils/page";
import { OrganizationPicker } from "../MCPServersPage/components/OrganizationPicker";
import {
	orgSearchParam,
	selectOrganization,
} from "../MCPServersPage/organizationParam";
import { readableSkillsOrganizations } from "../SkillsPage/readableSkillsOrganizations";
import { ForkOrganizationAgentDefinitionSharingDialog } from "./ForkOrganizationAgentDefinitionSharingDialog";

const ForkOrganizationAgentDefinitionsPage: React.FC = () => {
	const { permissions } = useAuthenticated();
	const { experiments, organizations } = useDashboard();
	const [searchParams, setSearchParams] = useSearchParams();
	const isEnabled = experiments.includes("fork-agent-definitions");
	const organizationPermissionsQuery = useQuery({
		...organizationsPermissions(
			organizations.map((organization) => organization.id),
		),
		enabled: isEnabled && permissions.viewAnyOrganizationSkills,
	});
	const [sharing, setSharing] = useState<{
		organizationId: string;
		name: string;
		onCloseAutoFocus: (event: Event) => void;
	}>();

	if (!isEnabled) {
		return <Navigate to="/ai/settings" replace />;
	}

	// Organization agent definitions share the organization skill
	// permissions on the server.
	const readableOrganizations = readableSkillsOrganizations(
		organizations,
		organizationPermissionsQuery.data,
	);
	const requestedOrganizationName = searchParams.get(orgSearchParam);
	const defaultOrganization =
		readableOrganizations.length > 0
			? selectOrganization(readableOrganizations, null)
			: undefined;
	// A requested organization the user cannot read is denied rather than
	// replaced, so a definition is never added to an organization the URL did
	// not name.
	const organization =
		requestedOrganizationName === null
			? defaultOrganization
			: readableOrganizations.find(
					(organization) => organization.name === requestedOrganizationName,
				);
	const organizationPermissions = organization
		? organizationPermissionsQuery.data?.[organization.id]
		: undefined;
	const canShare = Boolean(organizationPermissions?.shareOrganizationSkill);
	// Back and Forward can change the organization while the dialog is open,
	// and a save must not land in an organization the page no longer shows.
	if (sharing && sharing.organizationId !== organization?.id) {
		setSharing(undefined);
	}

	return (
		<RequirePermission isFeatureVisible={permissions.viewAnyOrganizationSkills}>
			<title>{pageTitle("Agent definitions", "AI Settings")}</title>
			{organizationPermissionsQuery.isLoadingError ? (
				<ErrorAlert error={organizationPermissionsQuery.error} />
			) : !organizationPermissionsQuery.data ? (
				<Loader />
			) : (
				<RequirePermission isFeatureVisible={Boolean(organization)}>
					{organizationPermissionsQuery.isRefetchError && (
						<ErrorAlert
							className="mb-4"
							error={organizationPermissionsQuery.error}
						/>
					)}
					{organization && (
						<ForkAgentDefinitionsTable
							scope={{ tier: "organization", organizationId: organization.id }}
							access={{
								create: Boolean(
									organizationPermissions?.createOrganizationSkill,
								),
								update: Boolean(
									organizationPermissions?.updateOrganizationSkill,
								),
								delete: Boolean(
									organizationPermissions?.deleteOrganizationSkill,
								),
							}}
							onManagePermissions={
								canShare
									? (definition, onCloseAutoFocus) =>
											setSharing({
												organizationId: organization.id,
												name: definition.name,
												onCloseAutoFocus,
											})
									: undefined
							}
							action={
								<OrganizationPicker
									id="agent-definitions-organization"
									className="w-full sm:w-60"
									organizations={readableOrganizations}
									organization={organization}
									onChange={(org) => {
										setSearchParams((params) => {
											const next = new URLSearchParams(params);
											next.set(orgSearchParam, org.name);
											return next;
										});
									}}
									showLabel={false}
								/>
							}
						/>
					)}
					{sharing && (
						<ForkOrganizationAgentDefinitionSharingDialog
							organizationId={sharing.organizationId}
							definitionName={sharing.name}
							onClose={() => setSharing(undefined)}
							onCloseAutoFocus={sharing.onCloseAutoFocus}
						/>
					)}
				</RequirePermission>
			)}
		</RequirePermission>
	);
};

export default ForkOrganizationAgentDefinitionsPage;
