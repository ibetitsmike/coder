// This file is fork code: AI settings sidebar entries for fork experiments,
// so AISettingsSidebarView.tsx, an upstream file, needs few lines.

import { useContext } from "react";
import type { To } from "react-router";
import type { Organization } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { orgSearchParam } from "#/pages/AISettingsPage/MCPServersPage/organizationParam";

/**
 * Returns the organization agent definitions page link, or undefined while
 * the experiment is off. Like the Skills link, it carries over only an
 * organization the page will show, since the page denies any other one.
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
			organizationName && isReadable
				? new URLSearchParams({ [orgSearchParam]: organizationName }).toString()
				: "",
	};
};
