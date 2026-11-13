// This file is fork code: it is the only place fork features add tool
// icons, so an upstream sync of ToolIcon.tsx stays clean.

import {
	AlarmClockIcon,
	BellIcon,
	HourglassIcon,
	ListTodoIcon,
	type LucideIcon,
	MessagesSquareIcon,
	SearchIcon,
	SendIcon,
	UsersIcon,
	WorkflowIcon,
} from "lucide-react";

/** Keys are fork iconNames, never tool names, so flag-off rows keep upstream's icon. */
export const forkToolIcons: Record<string, LucideIcon> = {
	fork_todo: ListTodoIcon,
	fork_search_chats: SearchIcon,
	fork_chat_transcript: MessagesSquareIcon,
	fork_wakeup: AlarmClockIcon,
	fork_wait_for_events: HourglassIcon,
	fork_notify_user: BellIcon,
	fork_workflow: WorkflowIcon,
	fork_family_message: SendIcon,
	fork_family_chats: UsersIcon,
};
