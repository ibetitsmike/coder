import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { render } from "#/testHelpers/renderHelpers";
import { ForkSettingsNavItems } from "./ForkSettingsNavItems";

const renderNavItems = (experiments: Experiment[]) =>
	render(
		<DashboardContext.Provider
			value={{
				entitlements: MockEntitlements,
				experiments,
				appearance: MockAppearanceConfig,
				buildInfo: MockBuildInfo,
				organizations: [MockDefaultOrganization],
				showOrganizations: false,
				canViewOrganizationSettings: false,
			}}
		>
			<ForkSettingsNavItems settingsSection="agent-definitions" state={null} />
		</DashboardContext.Provider>,
	);

describe("ForkSettingsNavItems", () => {
	it("links the agent definitions page when the experiment is on", () => {
		renderNavItems(["fork-agent-definitions"]);

		const link = screen.getByRole("link", { name: "Agent definitions" });
		expect(link.getAttribute("href")).toBe(
			"/agents/settings/agent-definitions",
		);
		expect(link.getAttribute("aria-current")).toBe("page");
	});

	it("renders nothing when the experiment is off", () => {
		renderNavItems([]);

		expect(screen.queryByRole("link")).toBeNull();
	});
});
