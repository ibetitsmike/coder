// This file is fork code: it renders the notify_user tool of the
// fork-chat-wakeups experiment.

import type React from "react";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import { asString, parseArgs, type ToolStatus } from "./utils";

const failedLabel = "Failed to send a notification";

type ForkNotifyUserToolProps = {
	title: string;
	message: string;
	status: ToolStatus;
	isError: boolean;
	errorMessage?: string;
};

const ForkNotifyUserTool: React.FC<ForkNotifyUserToolProps> = ({
	title,
	message,
	status,
	isError,
	errorMessage,
}) => {
	let label: string;
	if (status === "running") {
		label = "Sending a notification…";
	} else if (isError) {
		label = failedLabel;
	} else {
		label = "Sent a notification";
	}
	const errorDetail = errorMessage?.trim();

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedLabel}
			hasContent={title !== "" || message !== "" || Boolean(errorDetail)}
		>
			<ToolCall.Header iconName="fork_notify_user" label={label} />
			<ToolCall.Content>
				{errorDetail && (
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

/** The notification text comes from the args, the outcome from the result. */
export const ForkNotifyUserToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	const args = parseArgs(props.args);
	const title = asString(args?.title).trim();
	const message = asString(args?.message).trim();
	if (props.isError) {
		return (
			<ForkNotifyUserTool
				title={title}
				message={message}
				status={props.status}
				isError
				errorMessage={forkToolErrorMessage(props.result)}
			/>
		);
	}
	if (props.status !== "running" && parseArgs(props.result)?.sent !== true) {
		return <Fallback {...props} />;
	}
	return (
		<ForkNotifyUserTool
			title={title}
			message={message}
			status={props.status}
			isError={false}
		/>
	);
};
