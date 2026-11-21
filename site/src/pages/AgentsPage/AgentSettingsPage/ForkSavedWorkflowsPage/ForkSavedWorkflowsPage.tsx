// This file is fork code. Organization admins manage organization
// workflows in AI settings. Members share their personal workflows with the
// organization here and edit the ones they shared.

import { useEffect, useRef } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import {
	getOrganizationLabel,
	OrganizationAutocomplete,
} from "#/components/OrganizationAutocomplete/OrganizationAutocomplete";
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
	const [searchParams] = useSearchParams();
	const navigate = useNavigate();
	const experimentOn = experiments.includes("fork-workflows");

	// The URL names the organization the page shows. A param that names an
	// organization the member does not belong to, or an unknown one, renders
	// an unavailable state instead of falling back, so a stale or forged
	// link cannot quietly show another organization's workflows view.
	const requestedOrgName = searchParams.get(orgSearchParam);
	const organization = requestedOrgName
		? memberOrganizations.find((org) => org.name === requestedOrgName)
		: (memberOrganizations.find((org) => org.is_default) ??
			memberOrganizations.at(0));
	const isUnavailable = !organization && Boolean(requestedOrgName);
	// The page takes focus when the unavailable state mounts, so recovering
	// from a stale link starts at the page instead of the old document body.
	// It does not take focus back when the member picks from the picker: the
	// two states render the same element chain down to the picker, so the
	// trigger keeps its focus through the swap.
	const focusRef = useRef<HTMLDivElement>(null);
	useEffect(() => {
		if (isUnavailable) {
			focusRef.current?.focus();
		}
	}, [isUnavailable]);

	if (!experimentOn) {
		return <Navigate to="/agents/settings" replace />;
	}

	if (!organization) {
		if (requestedOrgName) {
			return (
				<div
					ref={focusRef}
					tabIndex={-1}
					data-testid="workflows-unavailable-focus"
					className="flex flex-col gap-10"
				>
					{/* Keep this picker's element chain identical to the
					    available state's, so choosing one keeps its focus
					    through the state swap. Without a member organization
					    there is nothing to pick. */}
					<div className="flex justify-end">
						<OrganizationAutocomplete
							ariaLabel="Organization Select an organization (optional)"
							value={null}
							options={memberOrganizations}
							triggerClassName="w-full sm:w-60"
							optionsTabbable
							disabled={memberOrganizations.length === 0}
							onChange={(org) => {
								// The picker write pushes an entry, so a
								// member can back out of a stale link to
								// the page's earlier state.
								if (org) {
									void navigate({
										search: `?${new URLSearchParams([
											[orgSearchParam, org.name],
										]).toString()}`,
									});
								}
							}}
						/>
					</div>
					<SectionHeader
						label="Workflows"
						description={`You are not a member of an organization named ${requestedOrgName}, so its workflows are not available here.${
							memberOrganizations.length > 0
								? " Pick one of your organizations instead."
								: ""
						}`}
					/>
				</div>
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
			    so it keeps its place and focus when the organization changes.
			    Its element chain matches the unavailable state's, so a pick
			    there keeps the trigger's focus through the state swap. */}
			<div className="flex justify-end">
				<OrganizationAutocomplete
					ariaLabel={`Organization ${getOrganizationLabel(
						organization,
						memberOrganizations,
					)}`}
					value={organization}
					options={memberOrganizations}
					triggerClassName="w-full sm:w-60"
					onChange={(org) => {
						// The picker write pushes an entry, so the
						// member can move back and forward between
						// the organizations they viewed. Reselecting
						// the shown organization pushes nothing, so
						// Back is not spent on a duplicate entry.
						if (org && org.id !== organization.id) {
							void navigate({
								search: `?${new URLSearchParams([
									[orgSearchParam, org.name],
								]).toString()}`,
							});
						}
					}}
				/>
			</div>
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
