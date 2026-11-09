// This file is fork code for the fork-chat-todos experiment. ForkComposerSlot
// renders it in the composer controls row.

import { cn } from "cn";
import {
	CircleCheckIcon,
	CircleDotIcon,
	CircleIcon,
	CircleXIcon,
	ListTodoIcon,
	type LucideIcon,
	TriangleAlertIcon,
} from "lucide-react";
import type React from "react";
import { useEffect } from "react";
import { useQuery, useQueryClient } from "react-query";
import { getErrorMessage } from "#/api/errors";
import { watchChatTodos } from "#/api/forkChatTodos";
import { chatTodos, chatTodosKey } from "#/api/queries/forkChatTodos";
import type { ChatTodo, ChatTodoStatus } from "#/api/typesGenerated";
import {
	Popover,
	PopoverContent,
	PopoverTrigger,
} from "#/components/Popover/Popover";
import { createReconnectingWebSocket } from "#/utils/reconnectingWebSocket";

const triggerClassName =
	"inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border-none bg-transparent px-1 text-xs outline-hidden transition-colors hover:bg-surface-secondary/60 focus-visible:ring-2 focus-visible:ring-content-link";

const popoverClassName =
	"mobile-full-width-dropdown w-76 p-3 text-xs font-normal text-content-primary";

const statusDisplay: Record<
	ChatTodoStatus,
	{ icon: LucideIcon; label: string; iconClassName: string }
> = {
	pending: {
		icon: CircleIcon,
		label: "Pending",
		iconClassName: "text-content-secondary",
	},
	in_progress: {
		icon: CircleDotIcon,
		label: "In progress",
		iconClassName: "text-content-link",
	},
	done: {
		icon: CircleCheckIcon,
		label: "Done",
		iconClassName: "text-content-success",
	},
	cancelled: {
		icon: CircleXIcon,
		label: "Cancelled",
		iconClassName: "text-content-disabled",
	},
};

/**
 * Shows the chat's todo list as a pill with a popover and keeps it live
 * through the todo watch socket. It renders nothing while the chat has no
 * todos.
 */
export const ChatTodoPill: React.FC<{ chatId: string }> = ({ chatId }) => {
	const queryClient = useQueryClient();
	const todosQuery = useQuery(chatTodos(chatId));
	const isLoaded = todosQuery.isSuccess;

	useEffect(() => {
		// Only watch a list the server let us read, so a chat without access
		// does not reconnect forever.
		if (!isLoaded) {
			return;
		}
		return createReconnectingWebSocket({
			connect() {
				const ws = watchChatTodos(chatId);
				ws.addEventListener("message", (event) => {
					if (event.parseError) {
						console.warn("Failed to parse chat todo event:", event.parseError);
						return;
					}
					queryClient.setQueryData(chatTodosKey(chatId), event.parsedMessage);
				});
				return ws;
			},
		});
	}, [chatId, isLoaded, queryClient]);

	return (
		<ChatTodoPillView todos={todosQuery.data?.todos} error={todosQuery.error} />
	);
};

type ChatTodoPillViewProps = {
	todos: readonly ChatTodo[] | undefined;
	error: unknown;
};

export const ChatTodoPillView: React.FC<ChatTodoPillViewProps> = ({
	todos,
	error,
}) => {
	if (todos) {
		return todos.length > 0 ? <ChatTodoListPill todos={todos} /> : null;
	}
	if (error) {
		return <ChatTodoErrorPill error={error} />;
	}
	return null;
};

const ChatTodoListPill: React.FC<{ todos: readonly ChatTodo[] }> = ({
	todos,
}) => {
	const counted = todos.filter((todo) => todo.status !== "cancelled");
	const done = counted.filter((todo) => todo.status === "done").length;
	const progress = `${done}/${counted.length}`;

	return (
		<Popover>
			<PopoverTrigger asChild>
				<button
					type="button"
					aria-label={`Todo list, ${progress} done`}
					className={triggerClassName}
				>
					<ListTodoIcon
						aria-hidden="true"
						className="size-3.5 shrink-0 text-content-secondary"
					/>
					<span className="text-content-secondary tabular-nums">
						{progress}
					</span>
				</button>
			</PopoverTrigger>
			<PopoverContent side="top" align="end" className={popoverClassName}>
				<h3 className="m-0 text-sm font-semibold">Todos</h3>
				<ul className="m-0 mt-2 flex list-none flex-col gap-1.5 p-0">
					{todos.map((todo) => (
						<ChatTodoItem key={todo.id} todo={todo} />
					))}
				</ul>
			</PopoverContent>
		</Popover>
	);
};

const ChatTodoItem: React.FC<{ todo: ChatTodo }> = ({ todo }) => {
	const { icon: Icon, label, iconClassName } = statusDisplay[todo.status];
	return (
		<li className="flex items-start gap-2">
			<Icon
				aria-hidden="true"
				className={cn("mt-0.5 size-3.5 shrink-0", iconClassName)}
			/>
			<span className="sr-only">{label}: </span>
			<span
				className={cn(
					"min-w-0 break-words leading-5",
					todo.status === "done" && "text-content-secondary",
					todo.status === "cancelled" && "text-content-secondary line-through",
				)}
			>
				{todo.content}
			</span>
		</li>
	);
};

const ChatTodoErrorPill: React.FC<{ error: unknown }> = ({ error }) => {
	return (
		<Popover>
			<PopoverTrigger asChild>
				<button
					type="button"
					aria-label="Todo list failed to load"
					className={triggerClassName}
				>
					<TriangleAlertIcon
						aria-hidden="true"
						className="size-3.5 shrink-0 text-content-warning"
					/>
				</button>
			</PopoverTrigger>
			<PopoverContent side="top" align="end" className={popoverClassName}>
				<h3 className="m-0 text-sm font-semibold">Todos</h3>
				<p className="mb-0 mt-2 text-content-secondary">
					{getErrorMessage(error, "Failed to load the todo list.")}
				</p>
			</PopoverContent>
		</Popover>
	);
};
