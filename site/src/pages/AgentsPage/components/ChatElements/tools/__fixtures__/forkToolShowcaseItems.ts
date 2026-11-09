// This file is fork code: it is the only place fork features add rows to
// the tool showcase in Tool.stories.tsx, so an upstream sync of that file
// stays clean.

import type { ComponentProps } from "react";
import { MockChatTodoList } from "#/testHelpers/forkChatTodos";
import type { Tool } from "../Tool";

type ForkToolShowcaseItem = Pick<
	ComponentProps<typeof Tool>,
	"name" | "args" | "result"
>;

/** Showcase rows for the tools that fork features add. */
export const forkToolShowcaseItems: readonly ForkToolShowcaseItem[] = [
	{
		name: "todo_write",
		args: { todos: MockChatTodoList.todos },
		result: MockChatTodoList,
	},
	{ name: "todo_read", result: MockChatTodoList },
];
