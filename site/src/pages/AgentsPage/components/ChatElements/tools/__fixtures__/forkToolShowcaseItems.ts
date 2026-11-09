// This file is fork code: it is the only place fork features add rows to
// the tool showcase in Tool.stories.tsx, so an upstream sync of that file
// stays clean.

import { MockChatTodoList } from "#/testHelpers/forkChatTodos";

/** Showcase rows for the tools that fork features add. */
export const forkToolShowcaseItems = [
	{
		name: "todo_write",
		args: { todos: MockChatTodoList.todos },
		result: MockChatTodoList,
	},
	{ name: "todo_read", result: MockChatTodoList },
];
