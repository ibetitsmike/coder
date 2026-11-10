// This file is fork code: it renders the todo_write and todo_read tools of
// the fork-chat-todos experiment.

import type React from "react";
import type { ChatTodo } from "#/api/typesGenerated";
import { ChatTodoChecklist, formatChatTodoProgress } from "../../ChatTodoPill";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage, parseChatTodo } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import { parseArgs, parseArray, type ToolStatus } from "./utils";

const actionLabels = {
	write: {
		running: "Updating todos…",
		failed: "Failed to update todos",
		empty: "Cleared todos",
		done: "Updated todos",
	},
	read: {
		running: "Reading todos…",
		failed: "Failed to read todos",
		empty: "No todos",
		done: "Read todos",
	},
};

type ForkTodoToolProps = {
	action: "write" | "read";
	todos: readonly ChatTodo[];
	status: ToolStatus;
	isError: boolean;
	errorMessage?: string;
};

const ForkTodoTool: React.FC<ForkTodoToolProps> = ({
	action,
	todos,
	status,
	isError,
	errorMessage,
}) => {
	const labels = actionLabels[action];
	let label: string;
	if (status === "running") {
		label = labels.running;
	} else if (isError) {
		label = labels.failed;
	} else if (todos.length === 0) {
		label = labels.empty;
	} else {
		label = `${labels.done} (${formatChatTodoProgress(todos)} done)`;
	}
	const errorDetail = errorMessage?.trim();
	const showTodos = todos.length > 0;

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || labels.failed}
			hasContent={showTodos || Boolean(errorDetail)}
		>
			<ToolCall.Header iconName="fork_todo" label={label} />
			<ToolCall.Content>
				{errorDetail && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{showTodos && (
					<ChatTodoChecklist
						todos={todos}
						className="mt-1.5 pl-6 text-[13px] text-content-primary"
					/>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};

/**
 * The stored list comes from the result; while todo_write runs, the
 * requested list comes from its args.
 */
export const ForkTodoToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	const action = props.name === "todo_write" ? "write" : "read";
	const resultRecord = parseArgs(props.result);
	if (props.isError) {
		return (
			// A failed write stored nothing, so its requested list stays hidden.
			<ForkTodoTool
				action={action}
				todos={[]}
				status={props.status}
				isError
				errorMessage={forkToolErrorMessage(props.result)}
			/>
		);
	}
	let todos: ChatTodo[] | null = null;
	if (resultRecord) {
		todos = parseArray(resultRecord.todos, parseChatTodo);
	} else if (props.status === "running") {
		// Streamed args omit todos until its array closes, so a running write
		// shows no list until then.
		todos = parseArray(parseArgs(props.args)?.todos, parseChatTodo) ?? [];
	}
	if (!todos) {
		return <Fallback {...props} />;
	}
	return (
		<ForkTodoTool
			action={action}
			todos={todos}
			status={props.status}
			isError={false}
		/>
	);
};
