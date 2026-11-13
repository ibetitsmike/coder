// This file is fork code: it renders the wait_for_events tool of the
// fork-chat-wakeups experiment.

import { ExternalLinkIcon } from "lucide-react";
import type React from "react";
import { DATE_FORMAT, formatDateTime } from "#/utils/time";
import { ForkChatLink, forkLinkRowClassName } from "./ForkChatLink";
import { parseSearchChatsEntry } from "./ForkSearchChatsTool";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import {
	asRecord,
	asString,
	parseArgs,
	parseArray,
	parseStringList,
} from "./utils";

const failedLabel = "Failed to wait for events";

type WaitProcess = {
	processId: string;
	command: string;
	outputPattern: string;
};

const parseWaitProcess = (item: unknown): WaitProcess | null => {
	const record = asRecord(item);
	if (!record || typeof record.process_id !== "string") {
		return null;
	}
	return {
		processId: record.process_id,
		command: asString(record.command),
		outputPattern: asString(record.output_pattern),
	};
};

const formatWaitTargets = (
	childCount: number,
	processCount: number,
	hasPullRequest: boolean,
) => {
	const targets: string[] = [];
	if (childCount > 0) {
		targets.push(
			childCount === 1 ? "1 child agent" : `${childCount} child agents`,
		);
	}
	if (processCount > 0) {
		targets.push(
			processCount === 1
				? "1 background process"
				: `${processCount} background processes`,
		);
	}
	if (hasPullRequest) {
		targets.push("a pull request");
	}
	return targets.length === 0
		? "Waiting for events"
		: `Waiting for ${new Intl.ListFormat("en-US").format(targets)}`;
};

/** The children, processes and pull request come from the result. */
export const ForkWaitForEventsToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	const { status, isError } = props;
	const result = isError ? null : parseArgs(props.result);
	const children = parseArray(result?.children, parseSearchChatsEntry);
	// The server omits processes when the call watches none.
	const processes =
		result?.processes === undefined
			? []
			: parseArray(result.processes, parseWaitProcess);
	if (!isError && (!children || !processes) && status !== "running") {
		return <Fallback {...props} />;
	}
	const pullRequest = asRecord(result?.pull_request);
	const pullRequestURL = asString(pullRequest?.url);
	const pullRequestEvents = parseStringList(pullRequest?.on) ?? [];
	const expiresAt = asString(result?.expires_at);
	const errorMessage = isError ? forkToolErrorMessage(props.result) : "";
	const errorDetail = errorMessage.trim();
	let label = formatWaitTargets(
		children?.length ?? 0,
		processes?.length ?? 0,
		pullRequestURL !== "",
	);
	if (status === "running") {
		label = "Starting to wait for events…";
	} else if (isError) {
		label = failedLabel;
	}

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedLabel}
			hasContent={Boolean(children) || errorDetail !== ""}
		>
			<ToolCall.Header iconName="fork_wait_for_events" label={label} />
			<ToolCall.Content>
				{errorDetail !== "" && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{children && (
					<div className="mt-1.5">
						{children.map((child) => (
							<ForkChatLink key={child.chatId} chatId={child.chatId}>
								{`${child.title || "Untitled"} (${child.status})`}
							</ForkChatLink>
						))}
						{processes?.map((process) => (
							<div
								key={process.processId}
								title={process.processId}
								className="text-[13px] text-content-secondary wrap-anywhere"
							>
								<span className="font-mono text-xs">
									{process.command || process.processId}
								</span>
								{process.outputPattern !== "" && (
									<>
										{" (lines matching "}
										<span className="font-mono text-xs">
											{process.outputPattern}
										</span>
										{")"}
									</>
								)}
							</div>
						))}
						{pullRequestURL !== "" && (
							<a
								href={pullRequestURL}
								target="_blank"
								rel="noreferrer"
								className={forkLinkRowClassName}
							>
								{pullRequestEvents.length > 0
									? `${pullRequestURL} (${pullRequestEvents.join(", ")})`
									: pullRequestURL}
								<ExternalLinkIcon className="size-3 shrink-0" />
							</a>
						)}
						{expiresAt !== "" && (
							<div className="text-[13px] text-content-secondary">
								{`Stops waiting ${formatDateTime(expiresAt, DATE_FORMAT.FULL_DATETIME)}`}
							</div>
						)}
					</div>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};
