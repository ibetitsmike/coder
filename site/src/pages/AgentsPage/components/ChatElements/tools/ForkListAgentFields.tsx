// This file is fork code: it adds the fields of the fork experiments to a
// row of the list_agents card.

import type React from "react";
import { formatChatTodoProgress } from "../../ChatTodoPill";
import { parseChatTodo, useForkExperiment } from "./forkToolUtils";
import { parseArray } from "./utils";

/**
 * Shows whether a listed agent waits for a subagent concurrency slot
 * (fork-nested-subagents) and its todo progress (fork-chat-todos).
 */
export const ForkListAgentFields: React.FC<{
	agent: Record<string, unknown>;
}> = ({ agent }) => {
	const nestedSubagents = useForkExperiment("fork-nested-subagents");
	const chatTodos = useForkExperiment("fork-chat-todos");
	const waiting = nestedSubagents && agent.waiting_for_capacity === true;
	const todos = chatTodos ? (parseArray(agent.todos, parseChatTodo) ?? []) : [];

	return (
		<>
			{waiting && (
				<span className="text-content-warning">
					{" · waiting for capacity"}
				</span>
			)}
			{todos.length > 0 && (
				<span>{` · ${formatChatTodoProgress(todos)} todos done`}</span>
			)}
		</>
	);
};
