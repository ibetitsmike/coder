import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it } from "vitest";
import type { Experiment } from "#/api/typesGenerated";
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
	isError = false,
	experiments = ["fork-family-messages"],
}: {
	name: "message_family_chat" | "list_family_chats";
	args?: Record<string, unknown>;
	result: unknown;
	isError?: boolean;
	experiments?: Experiment[];
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
						result={
							typeof result === "string" ? result : JSON.stringify(result)
						}
						isError={isError}
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
				experiments,
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
		await user.click(
			screen.getByRole("button", {
				name: "Sent message to Migrate chat sidebar to Tailwind",
			}),
		);
		await user.click(
			screen.getByRole("link", {
				name: "Migrate chat sidebar to Tailwind (waiting)",
			}),
		);
		expect(router.state.location.pathname).toBe(
			`/agents/${MockMessageFamilyChatResult.chat_id}`,
		);
	});

	it.each([
		{
			outcome: "queued",
			result: { ...MockMessageFamilyChatResult, queued: true },
			isError: false,
			label: /^Queued message for Migrate chat sidebar to Tailwind$/,
		},
		{
			outcome: "failed",
			result: "chat 22222222 is not in your chat family",
			isError: true,
			label: /^Failed to message chat/,
		},
	])("labels a $outcome message", ({ result, isError, label }) => {
		renderFamilyTool({
			name: "message_family_chat",
			args: MockMessageFamilyChatArgs,
			result,
			isError,
		});
		expect(screen.getAllByRole("button")[0]).toHaveAccessibleName(label);
	});

	it("keeps the upstream card when the experiment is off", () => {
		renderFamilyTool({
			name: "message_family_chat",
			args: MockMessageFamilyChatArgs,
			result: MockMessageFamilyChatResult,
			experiments: [],
		});
		expect(screen.getByRole("button")).not.toHaveAccessibleName(
			/sent message/i,
		);
	});

	it("lists family chats with their relation", async () => {
		renderFamilyTool({
			name: "list_family_chats",
			result: MockListFamilyChatsResult,
		});
		await userEvent.click(
			screen.getByRole("button", { name: "Found 3 family chats" }),
		);
		expect(screen.getAllByRole("link").map((link) => link.textContent)).toEqual(
			[
				"Ship family messages (parent, running)",
				"Migrate chat sidebar to Tailwind (sibling, waiting)",
			],
		);
		expect(screen.getByText("More chats exist")).toHaveTextContent(
			"More chats exist",
		);
	});
});
