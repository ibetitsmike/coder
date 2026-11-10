// This file is fork code: it renders the wait_for_events tool of the
// fork-chat-wakeups experiment.

import { ExternalLinkIcon } from "lucide-react";
import type React from "react";
import { DATE_FORMAT, formatDateTime } from "#/utils/time";
import { ForkChatLink } from "./ForkChatLink";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import {
	asRecord,
	asString,
	parseArgs,
	parseArray,
	parseStringList,
	type ToolStatus,
} from "./utils";

const failedLabel = "Failed to wait for events";

type WaitedChild = {
	chatId: string;
	title: string;
	status: string;
};

const parseWaitedChild = (item: unknown): WaitedChild | null => {
	const record = asRecord(item);
	if (!record || typeof record.chat_id !== "string") {
		return null;
	}
	return {
		chatId: record.chat_id,
		title: asString(record.title),
		status: asString(record.status),
	};
};

type WatchedPullRequest = {
	url: string;
	on: readonly string[];
};

const parseWatchedPullRequest = (value: unknown): WatchedPullRequest | null => {
	const record = asRecord(value);
	if (!record || typeof record.url !== "string") {
		return null;
	}
	return { url: record.url, on: parseStringList(record.on) ?? [] };
};

type EventsWait = {
	children: readonly WaitedChild[];
	pullRequest: WatchedPullRequest | null;
	expiresAt: string;
};

/** Names what the chat waits for, like "2 child agents and a pull request". */
const formatWaitTargets = ({ children, pullRequest }: EventsWait): string => {
	const targets: string[] = [];
	if (children.length > 0) {
		targets.push(
			children.length === 1
				? "1 child agent"
				: `${children.length} child agents`,
		);
	}
	if (pullRequest) {
		targets.push("a pull request");
	}
	return targets.join(" and ");
};

type ForkWaitForEventsToolProps = {
	wait?: EventsWait;
	status: ToolStatus;
	isError: boolean;
	errorMessage?: string;
};

const ForkWaitForEventsTool: React.FC<ForkWaitForEventsToolProps> = ({
	wait,
	status,
	isError,
	errorMessage,
}) => {
	let label: string;
	if (status === "running") {
		label = "Starting to wait for events…";
	} else if (isError) {
		label = failedLabel;
	} else {
		const targets = wait ? formatWaitTargets(wait) : "";
		label = targets === "" ? "Waiting for events" : `Waiting for ${targets}`;
	}
	const errorDetail = errorMessage?.trim();

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedLabel}
			hasContent={Boolean(wait) || Boolean(errorDetail)}
		>
			<ToolCall.Header iconName="fork_wait_for_events" label={label} />
			<ToolCall.Content>
				{errorDetail && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{wait && (
					<div className="mt-1.5">
						{wait.children.map((child) => (
							<ForkChatLink key={child.chatId} chatId={child.chatId}>
								{`${child.title || "Untitled"} (${child.status})`}
							</ForkChatLink>
						))}
						{wait.pullRequest && (
							<a
								href={wait.pullRequest.url}
								target="_blank"
								rel="noreferrer"
								className="flex w-fit items-center gap-1.5 text-[13px] text-content-secondary opacity-50 transition-opacity hover:opacity-100"
							>
								{wait.pullRequest.on.length > 0
									? `${wait.pullRequest.url} (${wait.pullRequest.on.join(", ")})`
									: wait.pullRequest.url}
								<ExternalLinkIcon className="size-3 shrink-0" />
							</a>
						)}
						{wait.expiresAt !== "" && (
							<div className="text-[13px] text-content-secondary">
								{`Stops waiting ${formatDateTime(wait.expiresAt, DATE_FORMAT.FULL_DATETIME)}`}
							</div>
						)}
					</div>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};

/** The children and the pull request come from the result. */
export const ForkWaitForEventsToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	if (props.isError) {
		return (
			<ForkWaitForEventsTool
				status={props.status}
				isError
				errorMessage={forkToolErrorMessage(props.result)}
			/>
		);
	}
	const result = parseArgs(props.result);
	const children = parseArray(result?.children, parseWaitedChild);
	if (!result || !children) {
		if (props.status === "running") {
			return <ForkWaitForEventsTool status={props.status} isError={false} />;
		}
		return <Fallback {...props} />;
	}
	return (
		<ForkWaitForEventsTool
			wait={{
				children,
				pullRequest: parseWatchedPullRequest(result.pull_request),
				expiresAt: asString(result.expires_at),
			}}
			status={props.status}
			isError={false}
		/>
	);
};
