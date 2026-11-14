// This file is fork code: tests for the fork-token-budget experiment's
// organization token budget section.

import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import * as forkTokenBudgetApi from "#/api/forkTokenBudget";
import { organizationTokenBudgetKey } from "#/api/queries/forkTokenBudget";
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
	it("saves the token budget with the summary fallback", async () => {
		const { updateSetting, queryClient } = renderSetting();
		const cancelQueries = vi.spyOn(queryClient, "cancelQueries");
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
				queryClient.getQueryData(organizationTokenBudgetKey(orgId)),
			).toStrictEqual(saved),
		);
		expect(cancelQueries).toHaveBeenCalledWith({
			queryKey: organizationTokenBudgetKey(orgId),
		});
	});

	it("announces a failed save", async () => {
		const { updateSetting } = renderSetting();
		updateSetting.mockRejectedValue(new Error("Invalid token budget setting."));
		const user = userEvent.setup();

		await user.click(
			within(await strategyGroup()).getByRole("radio", {
				name: "Token budget",
			}),
		);
		await user.click(screen.getByRole("button", { name: "Save" }));

		expect(await screen.findByRole("alert")).toHaveTextContent(
			"Invalid token budget setting.",
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
			queryClient.setQueryData(organizationTokenBudgetKey(orgId), {
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
		queryClient.setQueryData(organizationTokenBudgetKey(orgId), {
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

	it("is hidden while fork-token-budget is off", () => {
		const { getSetting } = renderSetting({ experiments: [] });

		expect(getSetting).not.toHaveBeenCalled();
		expect(screen.queryByText("Context compaction")).toBeNull();
	});

	it("does not request the setting for a member who cannot read it", () => {
		const { getSetting } = renderSetting({ canView: false });

		expect(getSetting).not.toHaveBeenCalled();
		expect(screen.queryByText("Context compaction")).toBeNull();
	});

	it("keeps a viewer's setting read-only", async () => {
		renderSetting({ canEdit: false });

		const radios = [
			...within(await strategyGroup()).getAllByRole("radio"),
			...within(await fallbackGroup()).getAllByRole("radio"),
		];
		for (const radio of radios) {
			expect(radio).toBeDisabled();
		}
		expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
	});

	it("enables the fallback only with the token budget strategy", async () => {
		renderSetting();
		const user = userEvent.setup();

		const fallbackRadios = within(await fallbackGroup()).getAllByRole("radio");
		for (const radio of fallbackRadios) {
			expect(radio).toBeDisabled();
		}
		await user.click(
			within(await strategyGroup()).getByRole("radio", {
				name: "Token budget",
			}),
		);
		for (const radio of fallbackRadios) {
			expect(radio).toBeEnabled();
		}
	});
});
