// This file is fork code: it is the only place fork features add rows to
// the tool showcase in Tool.stories.tsx, so an upstream sync of that file
// stays clean.

import type React from "react";
import {
	MockChatTodoList,
	MockChatTranscriptResult,
	MockSearchChatsResult,
} from "#/testHelpers/forkChatTodos";
import {
	MockNotifyUserArgs,
	MockScheduleWakeupArgs,
	MockScheduleWakeupResult,
	MockWaitForEventsResult,
} from "#/testHelpers/forkChatWakeups";
import {
	MockListFamilyChatsResult,
	MockMessageFamilyChatArgs,
	MockMessageFamilyChatResult,
} from "#/testHelpers/forkFamilyMessages";
import {
	MockForkWorkflowArgs,
	MockForkWorkflowCompletedResult,
	MockForkWorkflowRunId,
	MockForkWorkflowScript,
	MockForkWorkflowStartedResult,
} from "#/testHelpers/forkWorkflows";
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
		result: MockSearchChatsResult,
	},
	{ name: "read_chat_transcript", result: MockChatTranscriptResult },
	{
		name: "schedule_wakeup",
		args: MockScheduleWakeupArgs,
		result: MockScheduleWakeupResult,
	},
	{ name: "wait_for_events", result: MockWaitForEventsResult },
	{ name: "notify_user", args: MockNotifyUserArgs, result: { sent: true } },
	{
		name: "run_workflow",
		args: { script: MockForkWorkflowScript, args: MockForkWorkflowArgs },
		result: MockForkWorkflowStartedResult,
	},
	{
		name: "await_workflow",
		args: { run_id: MockForkWorkflowRunId },
		result: MockForkWorkflowCompletedResult,
	},
	{
		name: "message_family_chat",
		args: MockMessageFamilyChatArgs,
		result: MockMessageFamilyChatResult,
	},
	{ name: "list_family_chats", args: {}, result: MockListFamilyChatsResult },
];
