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
