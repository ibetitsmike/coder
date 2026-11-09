// This file is fork code: the fork-agent-definitions experiment's settings page.

import { useState } from "react";
import { Navigate } from "react-router";
import { OrganizationField } from "#/components/OrganizationAutocomplete/OrganizationAutocomplete";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import {
	getMemberOrganizations,
	useDashboard,
} from "#/modules/dashboard/useDashboard";
import { ForkAgentDefinitionsTable } from "./ForkAgentDefinitionsTable";

const ForkAgentDefinitionsPage: React.FC = () => {
	const { user } = useAuthenticated();
	const { experiments, organizations } = useDashboard();
	const memberOrganizations = getMemberOrganizations(organizations, user);
	const [selectedOrganizationId, setSelectedOrganizationId] =
		useState<string>();

	if (!experiments.includes("fork-agent-definitions")) {
		return <Navigate to="/agents/settings" replace />;
	}

	const organization =
		memberOrganizations.find((org) => org.id === selectedOrganizationId) ??
		memberOrganizations.find((org) => org.is_default) ??
		memberOrganizations.at(0);
	if (!organization) {
		return null;
	}

	return (
		<ForkAgentDefinitionsTable
			organizationId={organization.id}
			action={
				memberOrganizations.length > 1 && (
					<OrganizationField
						id="agent-definitions-organization"
						className="w-full sm:w-60"
						organizations={memberOrganizations}
						organization={organization}
						onChange={(org) => setSelectedOrganizationId(org.id)}
						showLabel={false}
					/>
				)
			}
		/>
	);
};

export default ForkAgentDefinitionsPage;
