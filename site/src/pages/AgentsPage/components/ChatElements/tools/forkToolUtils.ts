// This file is fork code: helpers shared by the fork tool cards.

import { useContext } from "react";
import {
	type ChatTodo,
	type ChatTodoStatus,
	ChatTodoStatuses,
	type Experiment,
} from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { asRecord, asString, parseArgs } from "./utils";

/** Returns the error text of a failed tool call's result. */
export const forkToolErrorMessage = (result: unknown): string => {
	const record = parseArgs(result);
	return record ? asString(record.error || record.message) : asString(result);
};

/** Reports whether the fork experiment that owns a tool card field is on. */
export const useForkExperiment = (experiment: Experiment): boolean => {
	// Tool also renders outside DashboardProvider, as in upstream's tool
	// stories, where fork fields stay off.
	const dashboard = useContext(DashboardContext);
	return dashboard?.experiments.includes(experiment) ?? false;
};

const isChatTodoStatus = (value: unknown): value is ChatTodoStatus =>
	ChatTodoStatuses.some((status) => status === value);

/** Parses one todo of a todo list in a tool result, or returns null. */
export const parseChatTodo = (item: unknown): ChatTodo | null => {
	const record = asRecord(item);
	if (
		!record ||
		typeof record.id !== "string" ||
		typeof record.content !== "string" ||
		!isChatTodoStatus(record.status)
	) {
		return null;
	}
	return { id: record.id, content: record.content, status: record.status };
};
