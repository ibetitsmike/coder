import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it } from "vitest";
import { chatDescendantQuestionsKey } from "#/api/queries/forkChatQuestions";
import type {
	ChatDescendantQuestion,
	CreateChatMessageRequest,
	Experiment,
} from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MOCK_TIMESTAMP, MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { server } from "#/testHelpers/server";
import { ForkDescendantQuestionDock } from "./ForkDescendantQuestionDock";

const mockOldestQuestion: ChatDescendantQuestion = {
	chat_id: "grandchild-chat",
	parent_chat_id: "child-chat",
	chat_title: "Migrate the billing tables",
	tool_call_id: "call-oldest",
	created_at: MOCK_TIMESTAMP,
	questions: [
		{
			header: "Backfill",
			question: "Should the migration backfill old rows?",
			options: [{ label: "Backfill every row" }, { label: "Skip old rows" }],
		},
	],
};

const mockNewerQuestion: ChatDescendantQuestion = {
	...mockOldestQuestion,
	chat_id: "other-child-chat",
	parent_chat_id: MockChat.id,
	chat_title: "Review the API",
	tool_call_id: "call-newer",
	created_at: "2024-01-02T00:00:00Z",
};

type RenderOptions = {
	experiments?: Experiment[];
	parentChatId?: string;
	isReadOnly?: boolean;
};

const renderDock = ({
	experiments = ["fork-nested-subagents"],
	parentChatId,
	isReadOnly = false,
}: RenderOptions = {}) => {
	const router = createMemoryRouter(
		[
			{
				path: "/agents/:agentId",
				element: (
					<ForkDescendantQuestionDock
						chat={{ id: MockChat.id, parent_chat_id: parentChatId }}
						isReadOnly={isReadOnly}
					/>
				),
			},
		],
		{ initialEntries: [`/agents/${MockChat.id}`] },
	);
	const queryClient = createTestQueryClient();
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
				<RouterProvider router={router} />
			</DashboardContext.Provider>
		</QueryClientProvider>,
	);
	return { router, queryClient };
};

/** Serves the pending questions and records the chats each request names. */
const serveQuestions = (questions: ChatDescendantQuestion[]) => {
	const questionRequests: string[] = [];
	const sentMessages: Array<{
		chatId: string;
		body: CreateChatMessageRequest;
	}> = [];
	server.use(
		http.get<{ chatId: string }>(
			"/api/v2/chats/fork/:chatId/questions",
			({ params }) => {
				questionRequests.push(params.chatId);
				return HttpResponse.json(questions);
			},
		),
		http.post<{ chatId: string }, CreateChatMessageRequest>(
			"/api/v2/chats/:chatId/messages",
			async ({ params, request }) => {
				sentMessages.push({
					chatId: params.chatId,
					body: await request.json(),
				});
				return HttpResponse.json({ queued: false });
			},
		),
	);
	return { questionRequests, sentMessages };
};

describe("ForkDescendantQuestionDock", () => {
	it.each<{ name: string; options: RenderOptions }>([
		{ name: "fork-nested-subagents is off", options: { experiments: [] } },
		{ name: "the chat is a child", options: { parentChatId: "parent-chat" } },
		{ name: "the chat is read-only", options: { isReadOnly: true } },
	])("does not request questions when $name", ({ options }) => {
		const { queryClient } = renderDock(options);

		expect(
			queryClient.getQueryState(chatDescendantQuestionsKey(MockChat.id)),
		).toBeUndefined();
	});

	it("sends the answer to the chat that asked the oldest question", async () => {
		const { questionRequests, sentMessages } = serveQuestions([
			mockOldestQuestion,
			mockNewerQuestion,
		]);
		const user = userEvent.setup();

		renderDock();

		await user.click(
			await screen.findByRole("radio", { name: /Skip old rows/ }),
		);
		await user.click(screen.getByRole("button", { name: "Submit" }));

		await waitFor(() =>
			expect(sentMessages).toEqual([
				{
					chatId: mockOldestQuestion.chat_id,
					body: {
						content: [
							{
								type: "text",
								text: "Answer to ask_user_question call call-oldest:\nSkip old rows",
							},
						],
					},
				},
			]),
		);
		await waitFor(() =>
			expect(questionRequests).toEqual([MockChat.id, MockChat.id]),
		);
	});

	it("links to the chat that asked the question", async () => {
		serveQuestions([mockOldestQuestion]);
		const user = userEvent.setup();

		const { router } = renderDock();

		await user.click(
			await screen.findByRole("link", { name: mockOldestQuestion.chat_title }),
		);

		expect(router.state.location.pathname).toBe(
			`/agents/${mockOldestQuestion.chat_id}`,
		);
	});
});
