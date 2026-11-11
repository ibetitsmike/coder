import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router";
import { expect, it } from "vitest";
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
	"links %s to the agent definitions page",
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
								experiments: ["fork-agent-definitions"],
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

		await user.click(screen.getByRole("link", { name: "Agent definitions" }));

		await waitFor(() =>
			expect(router.state.location).toMatchObject({
				pathname: "/ai/settings/agent-definitions",
				search,
			}),
		);
	},
);
