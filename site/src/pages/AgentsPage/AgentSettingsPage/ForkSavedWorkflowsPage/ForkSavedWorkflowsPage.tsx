// This file is fork code. Organization admins manage organization
// workflows in AI settings. Members share their personal workflows with the
// organization here and edit the ones they shared.

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

	if (!experiments.includes("fork-workflows")) {
		return <Navigate to="/agents/settings" replace />;
	}

	// The URL names the organization the page shows. A param that names an
	// organization the member does not belong to, or an unknown one, renders
	// an unavailable state instead of falling back, so a stale or forged
	// link cannot quietly show another organization's workflows view.
	const requestedOrgName = searchParams.get(orgSearchParam);
	const organization = requestedOrgName
		? memberOrganizations.find((org) => org.name === requestedOrgName)
		: (memberOrganizations.find((org) => org.is_default) ??
			memberOrganizations.at(0));
	if (!organization) {
		if (requestedOrgName) {
			return (
				<SectionHeader
					label="Workflows"
					description={`You are not a member of an organization named ${requestedOrgName}, so its workflows are not available here. Pick one of your organizations above the list instead.`}
				/>
			);
		}
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
