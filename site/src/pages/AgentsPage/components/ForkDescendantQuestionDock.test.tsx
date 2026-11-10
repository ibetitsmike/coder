import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { QueryClientProvider } from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it } from "vitest";
import { chatEntityKey } from "#/api/queries/chats";
import { chatDescendantQuestionsKey } from "#/api/queries/forkChatQuestions";
import type {
	ChatDescendantQuestion,
	CreateChatMessageRequest,
	Experiment,
} from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockChatDescendantQuestion } from "#/testHelpers/forkChatQuestions";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { server } from "#/testHelpers/server";
import { ForkDescendantQuestionDock } from "./ForkDescendantQuestionDock";

const mockOldestQuestion: ChatDescendantQuestion = {
	...MockChatDescendantQuestion,
	tool_call_id: "call-oldest",
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
	canAnswer?: boolean;
};

const renderDock = ({
	experiments = ["fork-nested-subagents"],
	parentChatId,
	isReadOnly = false,
	canAnswer = true,
}: RenderOptions = {}) => {
	const router = createMemoryRouter(
		[
			{
				path: "/agents/:agentId",
				element: (
					<ForkDescendantQuestionDock
						chat={{ id: MockChat.id, parent_chat_id: parentChatId }}
						isReadOnly={isReadOnly}
						canAnswer={canAnswer}
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

/**
 * Serves the pending questions and records the chats each request names.
 * Requests after the first get laterQuestions when it is set.
 */
const serveQuestions = (
	questions: ChatDescendantQuestion[],
	laterQuestions?: ChatDescendantQuestion[],
) => {
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
				return HttpResponse.json(
					questionRequests.length > 1 && laterQuestions
						? laterQuestions
						: questions,
				);
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

		const { queryClient } = renderDock();
		// The child's chat was opened before, so its entity is cached. The
		// test client's gcTime of 0 would drop the unobserved entry.
		queryClient.setQueryDefaults(chatEntityKey(mockOldestQuestion.chat_id), {
			gcTime: Number.POSITIVE_INFINITY,
		});
		queryClient.setQueryData(chatEntityKey(mockOldestQuestion.chat_id), {
			...MockChat,
			id: mockOldestQuestion.chat_id,
		});

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
			expect(questionRequests).toEqual([MockChat.id, MockChat.id, MockChat.id]),
		);
		expect(
			queryClient.getQueryState(chatEntityKey(mockOldestQuestion.chat_id))
				?.isInvalidated,
		).toBe(true);
	});

	it("does not send an answer to a question that is no longer pending", async () => {
		const { questionRequests, sentMessages } = serveQuestions(
			[mockOldestQuestion],
			[mockNewerQuestion],
		);
		const user = userEvent.setup();

		renderDock();

		await user.click(
			await screen.findByRole("radio", { name: /Skip old rows/ }),
		);
		await user.click(screen.getByRole("button", { name: "Submit" }));

		// The first poll, the check before the post, and the reload after it.
		await waitFor(() =>
			expect(questionRequests).toEqual([MockChat.id, MockChat.id, MockChat.id]),
		);
		expect(sentMessages).toEqual([]);
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

	it("sends a secret answer only to the secret answer route", async () => {
		const secretQuestion: ChatDescendantQuestion = {
			...mockOldestQuestion,
			tool_call_id: "call-secret",
			questions: [
				{
					header: "Token",
					question: "Paste the API token.",
					options: [],
					secret: true,
				},
			],
		};
		const { questionRequests, sentMessages } = serveQuestions([secretQuestion]);
		const secretAnswers: Array<{ chatId: string; body: unknown }> = [];
		server.use(
			http.post<{ chatId: string }>(
				"/api/v2/chats/fork/:chatId/questions/secret-answer",
				async ({ params, request }) => {
					secretAnswers.push({
						chatId: params.chatId,
						body: await request.json(),
					});
					return new HttpResponse(null, { status: 204 });
				},
			),
		);
		const user = userEvent.setup();

		renderDock();

		const field = await screen.findByLabelText(/Paste the API token/);
		expect(field).toHaveAttribute("type", "password");
		await user.type(field, "hunter2-secret-value");
		await user.click(screen.getByRole("button", { name: "Submit" }));

		// The status keeps the dock's focus, so the next question takes it.
		expect(await screen.findByRole("status")).toHaveFocus();
		expect(secretAnswers).toEqual([
			{
				chatId: secretQuestion.chat_id,
				body: { tool_call_id: "call-secret", value: "hunter2-secret-value" },
			},
		]);
		expect(sentMessages).toEqual([]);
		const stored = Object.keys(localStorage).map((key) =>
			localStorage.getItem(key),
		);
		expect(JSON.stringify(stored)).not.toContain("hunter2");
		await waitFor(() =>
			expect(questionRequests).toEqual([MockChat.id, MockChat.id]),
		);
	});
});
