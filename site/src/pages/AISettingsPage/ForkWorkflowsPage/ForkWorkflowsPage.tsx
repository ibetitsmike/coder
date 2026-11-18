// This file is fork code: the fork-workflows experiment's saved workflows
// page in AI settings. It shows the personal tier of the selected
// organization above the organization tier.

import { useState } from "react";
import { useQuery } from "react-query";
import { Navigate } from "react-router";
import { organizationsPermissions } from "#/api/queries/organizations";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Loader } from "#/components/Loader/Loader";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { RequirePermission } from "#/modules/permissions/RequirePermission";
import { OrganizationPicker } from "#/pages/AISettingsPage/MCPServersPage/components/OrganizationPicker";
import { useRequestedSkillsOrganization } from "#/pages/AISettingsPage/SkillsPage/forkRequestedSkillsOrganization";
import { readableSkillsOrganizations } from "#/pages/AISettingsPage/SkillsPage/readableSkillsOrganizations";
import { pageTitle } from "#/utils/page";
import { ForkOrganizationSavedWorkflowSharingDialog } from "./ForkOrganizationSavedWorkflowSharingDialog";
import { ForkSavedWorkflowsTable } from "./ForkSavedWorkflowsTable";

const ForkWorkflowsPage: React.FC = () => {
	const { permissions } = useAuthenticated();
	const { experiments, organizations } = useDashboard();
	const isEnabled = experiments.includes("fork-workflows");
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
	// Organization workflows share the organization skill permissions on the
	// server.
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

	const organizationPermissions = organization
		? organizationPermissionsQuery.data?.[organization.id]
		: undefined;
	// Back and Forward can change the organization while the dialog is open,
	// and a save must not land in an organization the page no longer shows.
	if (sharing && sharing.organizationId !== organization?.id) {
		setSharing(undefined);
	}

	return (
		<RequirePermission isFeatureVisible={permissions.viewAnyOrganizationSkills}>
			<title>{pageTitle("Workflows", "AI Settings")}</title>
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
						<div className="flex flex-col gap-10">
							<ForkSavedWorkflowsTable
								scope={{
									tier: "user",
									organizationId: organization.id,
								}}
								action={
									<OrganizationPicker
										id="workflows-organization"
										className="w-full sm:w-60"
										organizations={readableOrganizations}
										organization={organization}
										onChange={setOrganization}
										showLabel={false}
									/>
								}
							/>
							<ForkSavedWorkflowsTable
								scope={{
									tier: "organization",
									organizationId: organization.id,
								}}
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
									organizationPermissions?.shareOrganizationSkill
										? (workflow, onCloseAutoFocus) =>
												setSharing({
													organizationId: organization.id,
													name: workflow.name,
													onCloseAutoFocus,
												})
										: undefined
								}
							/>
						</div>
					)}
					{sharing && (
						<ForkOrganizationSavedWorkflowSharingDialog
							organizationId={sharing.organizationId}
							workflowName={sharing.name}
							onClose={() => setSharing(undefined)}
							onCloseAutoFocus={sharing.onCloseAutoFocus}
						/>
					)}
				</RequirePermission>
			)}
		</RequirePermission>
	);
};

export default ForkWorkflowsPage;
