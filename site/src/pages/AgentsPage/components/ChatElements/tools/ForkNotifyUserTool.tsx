// This file is fork code: it renders the notify_user tool of the
// fork-chat-wakeups experiment.

import type React from "react";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import { asString, parseArgs } from "./utils";

const failedLabel = "Failed to send a notification";

/** The notification text comes from the args, the outcome from the result. */
export const ForkNotifyUserToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	const { status, isError } = props;
	if (
		!isError &&
		status !== "running" &&
		parseArgs(props.result)?.sent !== true
	) {
		return <Fallback {...props} />;
	}
	const args = parseArgs(props.args);
	const title = asString(args?.title).trim();
	const message = asString(args?.message).trim();
	const errorMessage = isError ? forkToolErrorMessage(props.result) : "";
	const errorDetail = errorMessage.trim();
	let label = "Sent a notification";
	if (status === "running") {
		label = "Sending a notification…";
	} else if (isError) {
		label = failedLabel;
	}

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedLabel}
			hasContent={title !== "" || message !== "" || errorDetail !== ""}
		>
			<ToolCall.Header iconName="fork_notify_user" label={label} />
			<ToolCall.Content>
				{errorDetail !== "" && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{title !== "" && (
					<div className="mt-1.5 break-words text-[13px] font-medium text-content-primary">
						{title}
					</div>
				)}
				{message !== "" && (
					<div className="mt-1.5 whitespace-pre-wrap break-words text-[13px] text-content-primary">
						{message}
					</div>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};
