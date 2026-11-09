// This file is fork code: it renders the todo_write and todo_read tools of
// the fork-chat-todos experiment.

import type React from "react";
import {
	type ChatTodo,
	type ChatTodoStatus,
	ChatTodoStatuses,
} from "#/api/typesGenerated";
import { ChatTodoChecklist, formatChatTodoProgress } from "../../ChatTodoPill";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { ToolCall } from "./ToolCall";
import {
	asRecord,
	asString,
	parseArgs,
	parseArray,
	type ToolStatus,
} from "./utils";

const isChatTodoStatus = (value: unknown): value is ChatTodoStatus =>
	ChatTodoStatuses.some((status) => status === value);

const parseChatTodo = (item: unknown): ChatTodo | null => {
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

type ForkTodoToolProps = {
	action: "write" | "read";
	todos: readonly ChatTodo[];
	status: ToolStatus;
	isError: boolean;
	errorMessage?: string;
};

/**
 * Renders a todo_write or todo_read call as the todo checklist, collapsed
 * behind a header with the done count.
 */
const ForkTodoTool: React.FC<ForkTodoToolProps> = ({
	action,
	todos,
	status,
	isError,
	errorMessage,
}) => {
	const failureLabel =
		action === "write" ? "Failed to update todos" : "Failed to read todos";
	let label: string;
	if (status === "running") {
		label = action === "write" ? "Updating todos…" : "Reading todos…";
	} else if (isError) {
		label = failureLabel;
	} else if (todos.length === 0) {
		label = action === "write" ? "Cleared todos" : "No todos";
	} else {
		label = `${action === "write" ? "Updated" : "Read"} todos (${formatChatTodoProgress(todos)} done)`;
	}
	const errorDetail = isError ? errorMessage?.trim() : undefined;
	const showTodos = todos.length > 0;

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failureLabel}
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
		const errorMessage = resultRecord
			? asString(resultRecord.error || resultRecord.message)
			: asString(props.result);
		return (
			// A failed write stored nothing, so its requested list stays hidden.
			<ForkTodoTool
				action={action}
				todos={[]}
				status={props.status}
				isError
				errorMessage={errorMessage}
			/>
		);
	}
	let todos: ChatTodo[] | null = null;
	if (resultRecord) {
		todos = parseArray(resultRecord.todos, parseChatTodo);
	} else if (props.status === "running") {
		// Streamed args can end in a partial item, so skip invalid items.
		const argsTodos = parseArgs(props.args)?.todos;
		todos = Array.isArray(argsTodos)
			? argsTodos.map(parseChatTodo).filter((todo) => todo !== null)
			: [];
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
