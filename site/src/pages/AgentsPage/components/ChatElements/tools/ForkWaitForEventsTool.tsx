// This file is fork code: it renders the wait_for_events tool of the
// fork-chat-wakeups experiment.

import { cn } from "cn";
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

/** Names what the chat waits for, like "2 child agents and a pull request". */
const formatWaitTargets = (childCount: number, hasPullRequest: boolean) => {
	const targets: string[] = [];
	if (childCount > 0) {
		targets.push(
			childCount === 1 ? "1 child agent" : `${childCount} child agents`,
		);
	}
	if (hasPullRequest) {
		targets.push("a pull request");
	}
	return targets.length === 0
		? "Waiting for events"
		: `Waiting for ${targets.join(" and ")}`;
};

/** The children and the pull request come from the result. */
export const ForkWaitForEventsToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	const { status, isError } = props;
	const result = isError ? null : parseArgs(props.result);
	const children = parseArray(result?.children, parseSearchChatsEntry);
	if (!isError && !children && status !== "running") {
		return <Fallback {...props} />;
	}
	const pullRequest = asRecord(result?.pull_request);
	const pullRequestURL = asString(pullRequest?.url);
	const pullRequestEvents = parseStringList(pullRequest?.on) ?? [];
	const expiresAt = asString(result?.expires_at);
	const errorMessage = isError ? forkToolErrorMessage(props.result) : "";
	const errorDetail = errorMessage.trim();
	let label = formatWaitTargets(children?.length ?? 0, pullRequestURL !== "");
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
						{pullRequestURL !== "" && (
							<a
								href={pullRequestURL}
								target="_blank"
								rel="noreferrer"
								className={cn(forkLinkRowClassName, "max-w-full break-all")}
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
