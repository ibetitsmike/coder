import type { ChatTodoList } from "#/api/typesGenerated";

export const MockChatTodoList: ChatTodoList = {
	version: 3,
	todos: [
		{ id: "plan", content: "Plan the change", status: "done" },
		{ id: "build", content: "Build the todo pill", status: "in_progress" },
		{
			id: "test",
			content: "Cover the watch socket in Vitest",
			status: "pending",
		},
		{ id: "docs", content: "Rewrite the docs page", status: "cancelled" },
	],
};

export const MockSearchChatsResult = {
	chats: [
		{
			chat_id: "11111111-1111-4111-8111-111111111111",
			title: "Fix flaky e2e suite",
			status: "waiting",
			shared: true,
		},
		{
			chat_id: "22222222-2222-4222-8222-222222222222",
			title: "Migrate chat sidebar to Tailwind",
			status: "running",
		},
		{
			chat_id: "33333333-3333-4333-8333-333333333333",
			title: "",
			status: "error",
		},
	],
	has_more: false,
};

export const MockChatTranscriptResult = {
	chat_id: "11111111-1111-4111-8111-111111111111",
	title: "Fix flaky e2e suite",
	messages: [
		{
			id: 1,
			role: "user",
			parts: [{ type: "text", text: "Why is the e2e suite flaky on main?" }],
		},
		{
			id: 2,
			role: "assistant",
			parts: [
				{ type: "reasoning", text: "Compare the last green run with this one" },
			],
			omitted_parts: 2,
		},
		{
			id: 3,
			role: "tool",
			parts: [{ type: "tool-result", tool_name: "execute" }],
		},
	],
	has_more: false,
};
