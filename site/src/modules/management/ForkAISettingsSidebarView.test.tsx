import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import type { Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
	MockNoPermissions,
	MockOrganization2,
	MockPermissions,
} from "#/testHelpers/entities";
import { renderWithRouter } from "#/testHelpers/renderHelpers";
import AISettingsSidebarView from "./AISettingsSidebarView";

const selectedOrganizationSearch = `?org=${MockDefaultOrganization.name}`;

describe.each<[Experiment, string, string]>([
	[
		"fork-agent-definitions",
		"Agent definitions",
		"/ai/settings/agent-definitions",
	],
	["fork-plugin-packs", "Plugin packs", "/ai/settings/plugin-packs"],
])("the %s link", (experiment, linkName, pathname) => {
	it.each([
		[
			"a deployment admin",
			MockPermissions,
			[MockDefaultOrganization],
			selectedOrganizationSearch,
		],
		[
			"an organization auditor",
			{ ...MockNoPermissions, viewAnyOrganizationSkills: true },
			[MockDefaultOrganization],
			selectedOrganizationSearch,
		],
		[
			"a deployment admin while organization access loads",
			MockPermissions,
			[],
			selectedOrganizationSearch,
		],
		[
			"a user who cannot read the selected organization",
			MockPermissions,
			[MockOrganization2],
			"",
		],
	])(
		"links %s to the page",
		async (_, permissions, skillOrganizations, search) => {
			const user = userEvent.setup();
			const router = createMemoryRouter(
				[
					{
						path: "/ai/settings/*",
						element: (
							<DashboardContext.Provider
								value={{
									entitlements: MockEntitlements,
									experiments: [experiment],
									appearance: MockAppearanceConfig,
									buildInfo: MockBuildInfo,
									organizations: [MockDefaultOrganization],
									showOrganizations: false,
									canViewOrganizationSettings: false,
								}}
							>
								<AISettingsSidebarView
									permissions={permissions}
									canViewOrganizationInstructions={false}
									skillOrganizations={skillOrganizations}
								/>
							</DashboardContext.Provider>
						),
					},
				],
				{
					initialEntries: [`/ai/settings/skills${selectedOrganizationSearch}`],
				},
			);
			renderWithRouter(router);

			await user.click(screen.getByRole("link", { name: linkName }));

			await waitFor(() =>
				expect(router.state.location).toMatchObject({
					pathname,
					search,
				}),
			);
		},
	);
});
