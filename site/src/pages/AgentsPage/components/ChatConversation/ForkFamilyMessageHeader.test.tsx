import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { defaultUrlTransform } from "streamdown";
import { describe, expect, it, vi } from "vitest";
import type { Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import {
	MockFamilyMessage,
	MockFamilyMessageSenderId,
} from "#/testHelpers/forkFamilyMessages";
import { renderComponent } from "#/testHelpers/renderHelpers";
import { deriveMessageDisplayState } from "./messageHelpers";
import { parseMessageContent } from "./messageParsing";
import { UserMessageContent } from "./UserMessageContent";

const renderFamilyMessage = (experiments: Experiment[]) => {
	const parsed = parseMessageContent(MockFamilyMessage.content);
	const router = createMemoryRouter(
		[
			{
				path: "/agents/:agentId",
				element: (
					<UserMessageContent
						displayState={deriveMessageDisplayState({
							message: MockFamilyMessage,
							parsed,
							hideActions: false,
							hasActiveStream: false,
							isAwaitingFirstStreamChunk: false,
						})}
						markdown={parsed.markdown}
						isEditing={false}
						onImageClick={vi.fn()}
						onTextFileClick={vi.fn()}
						urlTransform={defaultUrlTransform}
					/>
				),
			},
		],
		{ initialEntries: ["/agents/recipient-chat"] },
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

describe("UserMessageContent family messages", () => {
	it("opens the sender chat from a family message's header", async () => {
		const router = renderFamilyMessage(["fork-family-messages"]);

		screen.getByText("Should I open the PR?");
		expect(screen.queryByText(/comes from another agent/)).toBeNull();
		await userEvent.click(screen.getByRole("link", { name: "Review the API" }));

		expect(router.state.location.pathname).toBe(
			`/agents/${MockFamilyMessageSenderId}`,
		);
	});

	it("shows the raw envelope when fork-family-messages is off", () => {
		renderFamilyMessage([]);

		screen.getByText(/comes from another agent/);
		screen.getByText(/<\/agent-message>/);
		expect(screen.queryByRole("link")).toBeNull();
	});
});
