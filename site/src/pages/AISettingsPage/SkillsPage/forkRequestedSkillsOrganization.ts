// This file is fork code: organization selection shared by the fork's
// organization skill-tier pages in AI settings.

import { useSearchParams } from "react-router";
import type { Organization } from "#/api/typesGenerated";
import {
	orgSearchParam,
	selectOrganization,
} from "../MCPServersPage/organizationParam";

/**
 * Resolves the organization named by the `org` search parameter among the
 * readable organizations, defaulting when the parameter is absent. A named
 * organization the user cannot read resolves to undefined rather than a
 * fallback, so a write never lands in an organization the URL did not name.
 */
export const useRequestedSkillsOrganization = (
	readableOrganizations: readonly Organization[],
) => {
	const [searchParams, setSearchParams] = useSearchParams();
	const requestedName = searchParams.get(orgSearchParam);
	let organization: Organization | undefined;
	if (requestedName !== null) {
		organization = readableOrganizations.find(
			(organization) => organization.name === requestedName,
		);
	} else if (readableOrganizations.length > 0) {
		organization = selectOrganization(readableOrganizations, null);
	}
	const setOrganization = (organization: Organization) => {
		setSearchParams((params) => {
			const next = new URLSearchParams(params);
			next.set(orgSearchParam, organization.name);
			return next;
		});
	};
	return { organization, setOrganization };
};
