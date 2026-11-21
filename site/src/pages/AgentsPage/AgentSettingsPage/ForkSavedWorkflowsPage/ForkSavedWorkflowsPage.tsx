// This file is fork code. Organization admins manage organization
// workflows in AI settings. Members share their personal workflows with the
// organization here and edit the ones they shared.

import { useState } from "react";
import { Navigate, useSearchParams } from "react-router";
import { OrganizationField } from "#/components/OrganizationAutocomplete/OrganizationAutocomplete";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import {
	getMemberOrganizations,
	useDashboard,
} from "#/modules/dashboard/useDashboard";
import { ForkSavedWorkflowsTable } from "#/pages/AISettingsPage/ForkWorkflowsPage/ForkSavedWorkflowsTable";
import { orgSearchParam } from "#/pages/AISettingsPage/MCPServersPage/organizationParam";
import { SectionHeader } from "../../components/SectionHeader";
import { readOnlySkillAccess, type SkillAccess } from "../../utils/skills";

// A member who shared a workflow keeps the right to edit and delete it,
// unless an admin revoked it. The server checks either way.
const sharedWorkflowAccess: SkillAccess = {
	create: false,
	update: true,
	delete: true,
};

const ForkSavedWorkflowsPage: React.FC = () => {
	const { user } = useAuthenticated();
	const { experiments, organizations } = useDashboard();
	const memberOrganizations = getMemberOrganizations(organizations, user);
	const [searchParams, setSearchParams] = useSearchParams();
	const [selectedOrganizationId, setSelectedOrganizationId] =
		useState<string>();

	if (!experiments.includes("fork-workflows")) {
		return <Navigate to="/agents/settings" replace />;
	}

	const organization =
		memberOrganizations.find((org) => org.id === selectedOrganizationId) ??
		memberOrganizations.find(
			(org) => org.name === searchParams.get(orgSearchParam),
		) ??
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
			{memberOrganizations.length > 1 && (
				<div className="flex justify-end">
					<OrganizationField
						id="workflows-organization"
						className="w-full sm:w-60"
						organizations={memberOrganizations}
						organization={organization}
						onChange={(org) => {
							setSelectedOrganizationId(org.id);
							setSearchParams(
								(prev) => {
									const next = new URLSearchParams(prev);
									next.set(orgSearchParam, org.name);
									return next;
								},
								{ replace: true },
							);
						}}
						showLabel={false}
					/>
				</div>
			)}
			<ForkSavedWorkflowsTable
				scope={{ tier: "user", organizationId: organization.id }}
				canShare
			/>
			<ForkSavedWorkflowsTable
				scope={{ tier: "organization", organizationId: organization.id }}
				access={readOnlySkillAccess}
				rowAccess={(workflow) =>
					workflow.created_by === user.id
						? sharedWorkflowAccess
						: readOnlySkillAccess
				}
				copy={{
					description:
						"Organization workflows you can read. Your chats in this organization run one with run_workflow. You can edit and delete the ones you shared. Ask an organization admin to change the others.",
					emptyMessage: "No organization workflows are available to you.",
				}}
			/>
		</div>
	);
};

export default ForkSavedWorkflowsPage;
