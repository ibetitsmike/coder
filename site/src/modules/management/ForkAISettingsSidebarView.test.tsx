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
	MockPermissions,
} from "#/testHelpers/entities";
import { renderWithRouter } from "#/testHelpers/renderHelpers";
import AISettingsSidebarView from "./AISettingsSidebarView";

it.each([
	["a deployment admin", MockPermissions, [MockDefaultOrganization]],
	[
		"an organization auditor",
		{ ...MockNoPermissions, viewAnyOrganizationSkills: true },
		[MockDefaultOrganization],
	],
	["a deployment admin while organization access loads", MockPermissions, []],
])(
	"links %s to the organization's agent definitions",
	async (_, permissions, skillOrganizations) => {
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
				initialEntries: [
					`/ai/settings/skills?org=${MockDefaultOrganization.name}`,
				],
			},
		);
		renderWithRouter(router);

		await user.click(screen.getByRole("link", { name: "Agent definitions" }));

		await waitFor(() =>
			expect(router.state.location).toMatchObject({
				pathname: "/ai/settings/agent-definitions",
				search: `?org=${MockDefaultOrganization.name}`,
			}),
		);
	},
);
