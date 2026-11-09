// This file is fork code: it is the only place fork features add tool
// icons, so an upstream sync of ToolIcon.tsx stays clean.

import { ListTodoIcon, type LucideIcon } from "lucide-react";

/**
 * Icons that fork tool cards pass as iconName. The keys are not tool names,
 * so while a fork experiment is off the generic row of its tool keeps
 * upstream's fallback icon.
 */
export const forkToolIcons: Record<string, LucideIcon> = {
	fork_todo: ListTodoIcon,
};
