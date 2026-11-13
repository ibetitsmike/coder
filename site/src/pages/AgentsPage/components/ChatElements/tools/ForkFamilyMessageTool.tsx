// This file is fork code: it renders the message_family_chat and
// list_family_chats tools of the fork-family-messages experiment.

import type React from "react";
import { ForkChatLink } from "./ForkChatLink";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import { asRecord, asString, parseArgs, parseArray } from "./utils";

const failedMessageLabel = "Failed to message chat";
const failedListLabel = "Failed to list family chats";

/** The sent text comes from the args, the recipient chat from the result. */
export const ForkMessageFamilyChatToolRenderer: React.FC<
	ForkToolRendererProps
> = ({ fallback: Fallback, ...props }) => {
	const { status, isError } = props;
	const message = asString(parseArgs(props.args)?.message).trim();
	const result = parseArgs(props.result);
	const recipientId = asString(result?.chat_id);
	if (!isError && status !== "running" && recipientId === "") {
		return <Fallback {...props} />;
	}
	const title = asString(result?.title) || "Untitled";
	const errorMessage = isError ? forkToolErrorMessage(props.result) : "";
	const errorDetail = errorMessage.trim();
	let label: string;
	if (status === "running") {
		label = "Messaging chat…";
	} else if (isError) {
		label = failedMessageLabel;
	} else if (result?.queued === true) {
		label = `Queued message for ${title}`;
	} else {
		label = `Sent message to ${title}`;
	}

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedMessageLabel}
			hasContent={recipientId !== "" || message !== "" || errorDetail !== ""}
		>
			<ToolCall.Header iconName="fork_family_message" label={label} />
			<ToolCall.Content>
				{errorDetail !== "" && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{!isError && recipientId !== "" && (
					<div className="mt-1.5">
						<ForkChatLink chatId={recipientId}>
							{`${title} (${asString(result?.status) || "unknown"})`}
						</ForkChatLink>
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

type FamilyChatEntry = {
	chatId: string;
	relation: string;
	status: string;
	title: string;
};

const parseFamilyChatEntry = (item: unknown): FamilyChatEntry | null => {
	const record = asRecord(item);
	if (!record || typeof record.chat_id !== "string") {
		return null;
	}
	return {
		chatId: record.chat_id,
		relation: asString(record.relation) || "unknown",
		status: asString(record.status) || "unknown",
		title: asString(record.title),
	};
};

/** Lists the caller's chat family; the caller's own row has no link. */
export const ForkListFamilyChatsToolRenderer: React.FC<
	ForkToolRendererProps
> = ({ fallback: Fallback, ...props }) => {
	const { status, isError } = props;
	const result = parseArgs(props.result);
	const chats = isError ? [] : parseArray(result?.chats, parseFamilyChatEntry);
	if (!chats && status !== "running") {
		return <Fallback {...props} />;
	}
	const rows = chats ?? [];
	const errorMessage = isError ? forkToolErrorMessage(props.result) : "";
	const errorDetail = errorMessage.trim();
	let label: string;
	if (status === "running") {
		label = "Listing family chats…";
	} else if (isError) {
		label = failedListLabel;
	} else if (rows.length === 1) {
		label = "Found 1 family chat";
	} else {
		label = `Found ${rows.length} family chats`;
	}

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedListLabel}
			hasContent={rows.length > 0 || errorDetail !== ""}
		>
			<ToolCall.Header iconName="fork_family_chats" label={label} />
			<ToolCall.Content>
				{errorDetail !== "" && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{rows.length > 0 && (
					<div className="mt-1.5">
						{rows.map((chat) => {
							const text = `${chat.title || "Untitled"} (${chat.relation}, ${chat.status})`;
							return chat.relation === "self" ? (
								<div
									key={chat.chatId}
									className="text-[13px] text-content-secondary"
								>
									{text}
								</div>
							) : (
								<ForkChatLink key={chat.chatId} chatId={chat.chatId}>
									{text}
								</ForkChatLink>
							);
						})}
						{result?.truncated === true && (
							<div className="text-[13px] text-content-secondary">
								More chats exist
							</div>
						)}
					</div>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};
