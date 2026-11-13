import { renderHook, waitFor } from "@testing-library/react";
import type { FC, PropsWithChildren } from "react";
import { QueryClientProvider } from "react-query";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
	MockUserMember,
} from "#/testHelpers/entities";
import { MockChatWorkflowRun } from "#/testHelpers/forkWorkflows";
import { createTestQueryClient } from "#/testHelpers/renderHelpers";
import { useForkWorkflowsTab } from "./useForkWorkflowsTab";

const Wrapper: FC<PropsWithChildren> = ({ children }) => (
	<QueryClientProvider client={createTestQueryClient()}>
		<DashboardContext.Provider
			value={{
				entitlements: MockEntitlements,
				experiments: ["fork-workflows"],
				appearance: MockAppearanceConfig,
				buildInfo: MockBuildInfo,
				organizations: [MockDefaultOrganization],
				showOrganizations: false,
				canViewOrganizationSettings: false,
			}}
		>
			{children}
		</DashboardContext.Provider>
	</QueryClientProvider>
);

describe("useForkWorkflowsTab", () => {
	it("shows the tab on a member's root chat with runs", async () => {
		const getRuns = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRuns")
			.mockResolvedValue([MockChatWorkflowRun]);
		const chat = { ...MockChat, owner_id: MockUserMember.id };

		const { result } = renderHook(() => useForkWorkflowsTab(chat), {
			wrapper: Wrapper,
		});

		await waitFor(() => expect(result.current).toBe(true));
		expect(getRuns).toHaveBeenCalledWith(chat.id, expect.anything());
	});

	it("never asks for runs on a child chat", () => {
		const getRuns = vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns");
		const chat = { ...MockChat, parent_chat_id: "parent-chat-id" };

		const { result } = renderHook(() => useForkWorkflowsTab(chat), {
			wrapper: Wrapper,
		});

		expect(result.current).toBe(false);
		expect(getRuns).not.toHaveBeenCalled();
	});
});
