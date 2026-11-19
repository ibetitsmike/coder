// This file is fork code. Organization admins manage organization
// workflows in AI settings, so members only read them here.

import { useState } from "react";
import { Navigate } from "react-router";
import { OrganizationField } from "#/components/OrganizationAutocomplete/OrganizationAutocomplete";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import {
	getMemberOrganizations,
	useDashboard,
} from "#/modules/dashboard/useDashboard";
import { ForkSavedWorkflowsTable } from "#/pages/AISettingsPage/ForkWorkflowsPage/ForkSavedWorkflowsTable";
import { SectionHeader } from "../../components/SectionHeader";
import { readOnlySkillAccess } from "../../utils/skills";

const ForkSavedWorkflowsPage: React.FC = () => {
	const { user } = useAuthenticated();
	const { experiments, organizations } = useDashboard();
	const memberOrganizations = getMemberOrganizations(organizations, user);
	const [selectedOrganizationId, setSelectedOrganizationId] =
		useState<string>();

	if (!experiments.includes("fork-workflows")) {
		return <Navigate to="/agents/settings" replace />;
	}

	const organization =
		memberOrganizations.find((org) => org.id === selectedOrganizationId) ??
		memberOrganizations.find((org) => org.is_default) ??
		memberOrganizations.at(0);
	if (!organization) {
		return (
			<SectionHeader
				label="Workflows"
				description="Join an organization to save workflows."
			/>
		);
	}

	return (
		<div className="flex flex-col gap-10">
			{/* The picker sits outside both tables, as on the AI settings page,
			    so it keeps its place and focus when the organization changes. */}
			<div className="flex justify-end">
				<OrganizationField
					id="workflows-organization"
					className="w-full sm:w-60"
					organizations={memberOrganizations}
					organization={organization}
					onChange={(org) => setSelectedOrganizationId(org.id)}
					showLabel={false}
				/>
			</div>
			<ForkSavedWorkflowsTable
				scope={{ tier: "user", organizationId: organization.id }}
			/>
			<ForkSavedWorkflowsTable
				scope={{ tier: "organization", organizationId: organization.id }}
				access={readOnlySkillAccess}
				copy={{
					description:
						"Organization workflows you can read. Your chats in this organization run one with run_workflow. Ask an organization admin to change them.",
					emptyMessage: "No organization workflows are available to you.",
				}}
			/>
		</div>
	);
};

export default ForkSavedWorkflowsPage;
