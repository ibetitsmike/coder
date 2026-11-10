// This file is fork code: it is the only place fork features add rows to
// the tool showcase in Tool.stories.tsx, so an upstream sync of that file
// stays clean.

import type React from "react";
import { MockChatTodoList } from "#/testHelpers/forkChatTodos";
import type { Tool } from "../Tool";

type ForkToolShowcaseItem = Pick<
	React.ComponentProps<typeof Tool>,
	"name" | "args" | "result"
>;

/** Tool.stories.tsx spreads these rows so every fork renderer has a row. */
export const forkToolShowcaseItems: readonly ForkToolShowcaseItem[] = [
	{
		name: "todo_write",
		args: { todos: MockChatTodoList.todos },
		result: MockChatTodoList,
	},
	{ name: "todo_read", result: MockChatTodoList },
	{
		name: "search_chats",
		args: { query: "status:waiting" },
		result: {
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
			],
			has_more: false,
		},
	},
	{
		name: "read_chat_transcript",
		result: {
			chat_id: "11111111-1111-4111-8111-111111111111",
			title: "Fix flaky e2e suite",
			messages: [
				{
					role: "user",
					parts: [
						{ type: "text", text: "Why is the e2e suite flaky on main?" },
					],
				},
				{
					role: "assistant",
					parts: [{ type: "tool-call", tool_name: "execute" }],
					omitted_parts: 3,
				},
			],
			has_more: true,
		},
	},
];
