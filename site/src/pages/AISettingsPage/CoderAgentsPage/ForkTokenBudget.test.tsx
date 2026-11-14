// This file is fork code: tests for the fork-token-budget experiment's
// organization token budget section.

import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkTokenBudgetApi from "#/api/forkTokenBudget";
import { organizationTokenBudget } from "#/api/queries/forkTokenBudget";
import { organizationsPermissions } from "#/api/queries/organizations";
import type { Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockForkOrganizationTokenBudget } from "#/testHelpers/forkTokenBudget";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { ForkOrganizationAgentSettings } from "./ForkOrganizationAgentSettings";

const orgId = MockDefaultOrganization.id;

type RenderOptions = {
	experiments?: Experiment[];
	canView?: boolean;
	canEdit?: boolean;
};

const renderSetting = ({
	experiments = ["fork-token-budget"],
	canView = true,
	canEdit = true,
}: RenderOptions = {}) => {
	const getSetting = vi
		.spyOn(forkTokenBudgetApi, "getOrganizationTokenBudget")
		.mockResolvedValue(MockForkOrganizationTokenBudget);
	const updateSetting = vi
		.spyOn(forkTokenBudgetApi, "updateOrganizationTokenBudget")
		.mockImplementation(async (_, req) => req);
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
				<ForkOrganizationAgentSettings
					organization={MockDefaultOrganization}
					canEdit={canEdit}
				/>
			</DashboardContext.Provider>
		</QueryClientProvider>,
	);
	return { getSetting, updateSetting, queryClient };
};

const strategyGroup = () =>
	screen.findByRole("radiogroup", { name: "Strategy" });
const fallbackGroup = () =>
	screen.findByRole("radiogroup", { name: "Fallback at the usable limit" });

afterEach(() => {
	vi.restoreAllMocks();
});

describe("ForkTokenBudget", () => {
	it("shows the defaults with the fallback disabled", async () => {
		renderSetting();

		const strategy = await strategyGroup();
		expect(
			within(strategy).getByRole("radio", {
				name: "Summarize the conversation",
			}),
		).toBeChecked();
		const fallback = await fallbackGroup();
		const newWindow = within(fallback).getByRole("radio", {
			name: "Start a new context window",
		});
		expect(newWindow).toBeChecked();
		expect(newWindow).toBeDisabled();
		expect(
			screen.getByText(/each model's compression threshold/),
		).toHaveTextContent("A threshold of 100% turns the budget off");
	});

	it("saves the token budget with the summary fallback", async () => {
		const { updateSetting, queryClient } = renderSetting();
		const user = userEvent.setup();

		await user.click(
			within(await strategyGroup()).getByRole("radio", {
				name: "Token budget",
			}),
		);
		await user.click(
			within(await fallbackGroup()).getByRole("radio", {
				name: "Summarize the conversation",
			}),
		);
		await user.click(screen.getByRole("button", { name: "Save" }));

		const saved = { strategy: "token_budget", fallback: "summary" };
		expect(updateSetting.mock.lastCall).toStrictEqual([orgId, saved]);
		await waitFor(() =>
			expect(
				queryClient.getQueryData(organizationTokenBudget(orgId).queryKey),
			).toStrictEqual(saved),
		);
	});

	it("keeps an unsaved edit when the setting is refetched", async () => {
		const { queryClient } = renderSetting();
		const user = userEvent.setup();

		const tokenBudget = within(await strategyGroup()).getByRole("radio", {
			name: "Token budget",
		});
		await user.click(tokenBudget);
		await act(async () => {
			queryClient.setQueryData(organizationTokenBudget(orgId).queryKey, {
				strategy: "summary",
				fallback: "summary",
			});
		});

		expect(tokenBudget).toBeChecked();
		expect(
			within(await fallbackGroup()).getByRole("radio", {
				name: "Start a new context window",
			}),
		).toBeChecked();
	});

	it("refreshes an unedited form when the setting is refetched", async () => {
		const { queryClient } = renderSetting();

		const fallback = await fallbackGroup();
		queryClient.setQueryData(organizationTokenBudget(orgId).queryKey, {
			strategy: "token_budget",
			fallback: "summary",
		});

		await waitFor(() =>
			expect(
				within(fallback).getByRole("radio", {
					name: "Summarize the conversation",
				}),
			).toBeChecked(),
		);
	});

	it("keeps a viewer's setting read-only", async () => {
		renderSetting({ canEdit: false });

		const tokenBudget = within(await strategyGroup()).getByRole("radio", {
			name: "Token budget",
		});
		expect(tokenBudget).toBeDisabled();
		expect(
			screen.getByText(/You can view this setting but not change it/),
		).toBeInTheDocument();
	});

	it("is hidden while fork-token-budget is off", () => {
		const { getSetting } = renderSetting({ experiments: [] });

		expect(getSetting).not.toHaveBeenCalled();
		expect(
			screen.queryByRole("form", { name: "Context compaction" }),
		).not.toBeInTheDocument();
	});

	it("does not request the setting for a member who cannot read it", () => {
		const { getSetting } = renderSetting({ canView: false });

		expect(getSetting).not.toHaveBeenCalled();
	});
});
