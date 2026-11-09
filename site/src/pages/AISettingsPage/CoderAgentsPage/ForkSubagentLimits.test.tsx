// This file is fork code: tests for the fork-nested-subagents experiment's
// organization subagent limits section.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkSubagentLimitsApi from "#/api/forkSubagentLimits";
import { organizationsPermissions } from "#/api/queries/organizations";
import type { Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockForkOrganizationSubagentLimits } from "#/testHelpers/forkSubagentLimits";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { ForkSubagentLimits } from "./ForkSubagentLimits";

const orgId = MockDefaultOrganization.id;

type RenderOptions = {
	experiments?: Experiment[];
	canView?: boolean;
	canEdit?: boolean;
};

const renderLimits = ({
	experiments = ["fork-nested-subagents"],
	canView = true,
	canEdit = true,
}: RenderOptions = {}) => {
	const getLimits = vi
		.spyOn(forkSubagentLimitsApi, "getOrganizationSubagentLimits")
		.mockResolvedValue(MockForkOrganizationSubagentLimits);
	const updateLimits = vi
		.spyOn(forkSubagentLimitsApi, "updateOrganizationSubagentLimits")
		.mockResolvedValue(MockForkOrganizationSubagentLimits);
	vi.spyOn(API, "checkAuthorization").mockResolvedValue({
		[`${orgId}.viewChatModelConfigs`]: canView,
	});
	const queryClient = createTestQueryClient();
	// The Coder Agents page fetches these permissions before it renders the
	// section, so they are already cached when it mounts.
	queryClient.setQueryData(organizationsPermissions([orgId]).queryKey, {
		[orgId]: { viewChatModelConfigs: canView },
	});
	renderComponent(
		<QueryClientProvider client={queryClient}>
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
				<ForkSubagentLimits
					organization={MockDefaultOrganization}
					canEdit={canEdit}
				/>
			</DashboardContext.Provider>
		</QueryClientProvider>,
	);
	return { getLimits, updateLimits };
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkSubagentLimits", () => {
	it("saves a changed limit with the other set limits", async () => {
		const { updateLimits } = renderLimits();
		const user = userEvent.setup();

		await user.type(
			await screen.findByLabelText("Live subagents per chat family"),
			"2",
		);
		await user.click(screen.getByRole("button", { name: "Save" }));

		expect(updateLimits.mock.lastCall).toStrictEqual([
			orgId,
			{ max_depth: 2, max_concurrent: 10, max_live_per_family: 2 },
		]);
	});

	it("omits a cleared limit from the request", async () => {
		const { updateLimits } = renderLimits();
		const user = userEvent.setup();

		await user.clear(await screen.findByLabelText("Max depth"));
		await user.click(screen.getByRole("button", { name: "Save" }));

		expect(updateLimits.mock.lastCall).toStrictEqual([
			orgId,
			{ max_concurrent: 10 },
		]);
	});

	it.each([
		{ label: "Max depth", value: "17" },
		{ label: "Live subagents per chat family", value: "0" },
	])("does not save $label set to $value", async ({ label, value }) => {
		const { updateLimits } = renderLimits();
		const user = userEvent.setup();

		const input = await screen.findByLabelText(label);
		await user.clear(input);
		await user.type(input, value);
		await user.click(screen.getByRole("button", { name: "Save" }));

		expect(updateLimits).not.toHaveBeenCalled();
	});

	it("does not let a viewer change the limits", async () => {
		renderLimits({ canEdit: false });
		const user = userEvent.setup();

		const input = await screen.findByLabelText(
			"Live subagents per chat family",
		);
		await user.type(input, "2");

		expect(input).toHaveValue(null);
	});

	it("does not request the limits while fork-nested-subagents is off", () => {
		const { getLimits } = renderLimits({ experiments: [] });

		expect(getLimits).not.toHaveBeenCalled();
	});

	it("does not request the limits for a member who cannot read them", () => {
		const { getLimits } = renderLimits({ canView: false });

		expect(getLimits).not.toHaveBeenCalled();
	});
});
