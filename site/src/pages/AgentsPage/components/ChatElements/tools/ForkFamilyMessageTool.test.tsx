import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it } from "vitest";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import {
	MockListFamilyChatsResult,
	MockMessageFamilyChatArgs,
	MockMessageFamilyChatResult,
} from "#/testHelpers/forkFamilyMessages";
import { renderComponent } from "#/testHelpers/renderHelpers";
import { Tool } from "./Tool";

const renderFamilyTool = ({
	name,
	args = {},
	result,
}: {
	name: "message_family_chat" | "list_family_chats";
	args?: Record<string, unknown>;
	result: unknown;
}) => {
	const router = createMemoryRouter(
		[
			{
				path: "/agents/:agentId",
				element: (
					<Tool
						name={name}
						status="completed"
						args={args}
						result={JSON.stringify(result)}
						isError={false}
						organizationId="organization-id"
						mcpServers={[]}
						subagentTitles={new Map()}
						subagentVariants={new Map()}
						shellToolDisplayMode="auto"
						codeDiffDisplayMode="auto"
					/>
				),
			},
		],
		{ initialEntries: ["/agents/33333333-3333-4333-8333-333333333333"] },
	);
	renderComponent(
		<DashboardContext.Provider
			value={{
				entitlements: MockEntitlements,
				experiments: ["fork-family-messages"],
				appearance: MockAppearanceConfig,
				buildInfo: MockBuildInfo,
				organizations: [MockDefaultOrganization],
				showOrganizations: false,
				canViewOrganizationSettings: false,
			}}
		>
			<RouterProvider router={router} />
		</DashboardContext.Provider>,
	);
	return router;
};

describe("ForkFamilyMessageTool", () => {
	it("opens the recipient chat of a sent message", async () => {
		const router = renderFamilyTool({
			name: "message_family_chat",
			args: MockMessageFamilyChatArgs,
			result: MockMessageFamilyChatResult,
		});
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { expanded: false }));
		await user.click(screen.getByRole("link"));
		expect(router.state.location.pathname).toBe(
			`/agents/${MockMessageFamilyChatResult.chat_id}`,
		);
	});

	it("opens a family chat from its row", async () => {
		const router = renderFamilyTool({
			name: "list_family_chats",
			result: MockListFamilyChatsResult,
		});
		const sibling = MockListFamilyChatsResult.chats[1];
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { expanded: false }));
		await user.click(
			screen.getByRole("link", { name: new RegExp(sibling.title) }),
		);
		expect(router.state.location.pathname).toBe(`/agents/${sibling.chat_id}`);
	});
});
