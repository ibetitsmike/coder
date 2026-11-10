// This file is fork code: it is the only place fork features add tool
// icons, so an upstream sync of ToolIcon.tsx stays clean.

import {
	ListTodoIcon,
	type LucideIcon,
	MessagesSquareIcon,
	SearchIcon,
} from "lucide-react";

/** Keys are fork iconNames, never tool names, so flag-off rows keep upstream's icon. */
export const forkToolIcons: Record<string, LucideIcon> = {
	fork_todo: ListTodoIcon,
	fork_search_chats: SearchIcon,
	fork_chat_transcript: MessagesSquareIcon,
};
