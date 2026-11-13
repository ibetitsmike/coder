import { renderHook, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { defaultUrlTransform } from "streamdown";
import { describe, expect, it, vi } from "vitest";
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
import { getAsyncAnswerPrefix } from "../ChatElements/tools/asyncAnswer";
import { useForkFamilyMessage } from "./ForkFamilyMessageHeader";
import { deriveMessageDisplayState } from "./messageHelpers";
import { parseMessageContent } from "./messageParsing";
import { UserMessageContent } from "./UserMessageContent";

const WithFamilyMessages: React.FC<React.PropsWithChildren> = ({
	children,
}) => (
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
		{children}
	</DashboardContext.Provider>
);

describe("UserMessageContent family messages", () => {
	it("opens the sender chat from a family message's header", async () => {
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
			<WithFamilyMessages>
				<RouterProvider router={router} />
			</WithFamilyMessages>,
		);

		await userEvent.click(screen.getByRole("link", { name: "Review the API" }));

		expect(router.state.location.pathname).toBe(
			`/agents/${MockFamilyMessageSenderId}`,
		);
	});

	it("does not read a family message body as an async answer", () => {
		const body = `${getAsyncAnswerPrefix("question")}Backend first.`;
		const splitAsyncAnswer = vi.fn();
		const { result } = renderHook(
			() =>
				useForkFamilyMessage(
					[
						{
							type: "response",
							text: `<agent-message from_chat_id="${MockFamilyMessageSenderId}" from_title="Sender">\nPreamble.\n\n${body}\n</agent-message>`,
						},
					],
					splitAsyncAnswer,
				),
			{ wrapper: WithFamilyMessages },
		);

		expect(splitAsyncAnswer).not.toHaveBeenCalled();
		expect(result.current.isAsyncAnswer).toBe(false);
		expect(result.current.blocks).toEqual([{ type: "response", text: body }]);
	});
});
