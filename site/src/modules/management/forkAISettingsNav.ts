// This file is fork code: AI settings sidebar entries for fork experiments,
// so AISettingsSidebarView.tsx, an upstream file, needs few lines.

import { useContext } from "react";
import type { To } from "react-router";
import type { Organization } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { orgSearchParam } from "#/pages/AISettingsPage/MCPServersPage/organizationParam";

/**
 * Returns the organization agent definitions page link, or undefined while
 * the experiment is off. It carries over the selected organization unless
 * the user is known not to read its skills, since the page denies that
 * organization. While access is still loading the organization is kept, so
 * the page never falls back to its default organization instead.
 * Stories render the sidebar outside DashboardProvider, where it is hidden.
 */
export const useForkAgentDefinitionsPath = (
	skillOrganizations: readonly Organization[],
	organizationName: string | null,
): To | undefined => {
	const dashboard = useContext(DashboardContext);
	if (!dashboard?.experiments.includes("fork-agent-definitions")) {
		return undefined;
	}
	const isReadable = skillOrganizations.some(
		(organization) => organization.name === organizationName,
	);
	return {
		pathname: "/ai/settings/agent-definitions",
		search:
			organizationName && (skillOrganizations.length === 0 || isReadable)
				? new URLSearchParams({ [orgSearchParam]: organizationName }).toString()
				: "",
	};
};
