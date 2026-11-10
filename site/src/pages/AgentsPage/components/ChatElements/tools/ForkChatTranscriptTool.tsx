// This file is fork code: it renders the read_chat_transcript tool of the
// fork-chat-todos experiment.

import type React from "react";
import { ForkChatLink } from "./ForkChatLink";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import {
	asNumber,
	asRecord,
	asString,
	parseArgs,
	parseArray,
	type ToolStatus,
} from "./utils";

const failedLabel = "Failed to read chat transcript";

type TranscriptMessage = {
	role: string;
	preview: string;
	omittedParts: number;
};

const partPreview = (part: Record<string, unknown> | null): string => {
	const type = asString(part?.type);
	if (type === "text" || type === "reasoning") {
		return asString(part?.text);
	}
	if (type === "tool-call" || type === "tool-result") {
		return asString(part?.tool_name);
	}
	return type;
};

const parseTranscriptMessage = (item: unknown): TranscriptMessage | null => {
	const record = asRecord(item);
	if (!record || typeof record.role !== "string") {
		return null;
	}
	const parts = Array.isArray(record.parts) ? record.parts : [];
	return {
		role: record.role,
		preview: partPreview(asRecord(parts[0])),
		omittedParts: asNumber(record.omitted_parts) ?? 0,
	};
};

type ForkChatTranscriptToolProps = {
	chatId?: string;
	title?: string;
	messages: readonly TranscriptMessage[];
	hasMore: boolean;
	status: ToolStatus;
	isError: boolean;
	errorMessage?: string;
};

const ForkChatTranscriptTool: React.FC<ForkChatTranscriptToolProps> = ({
	chatId,
	title,
	messages,
	hasMore,
	status,
	isError,
	errorMessage,
}) => {
	const chatTitle = title || "Untitled";
	let label: string;
	if (status === "running") {
		label = "Reading chat transcript…";
	} else if (isError) {
		label = failedLabel;
	} else {
		const noun = messages.length === 1 ? "message" : "messages";
		label = `Read ${messages.length} ${noun} from ${chatTitle}`;
	}
	const errorDetail = errorMessage?.trim();

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedLabel}
			hasContent={
				messages.length > 0 || Boolean(chatId) || Boolean(errorDetail)
			}
		>
			<ToolCall.Header iconName="fork_chat_transcript" label={label} />
			<ToolCall.Content>
				{errorDetail && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{chatId && (
					<div className="mt-1.5">
						<ForkChatLink chatId={chatId}>{chatTitle}</ForkChatLink>
					</div>
				)}
				{messages.length > 0 && (
					<div className="mt-1.5">
						{messages.map((message, index) => (
							<div
								key={index}
								className="flex items-baseline gap-1.5 text-[13px] text-content-secondary"
							>
								<span className="shrink-0">{message.role}</span>
								<span className="min-w-0 flex-1 truncate">
									{message.preview}
								</span>
								{message.omittedParts > 0 && (
									<span className="shrink-0">
										{`(+${message.omittedParts} ${message.omittedParts === 1 ? "part" : "parts"} omitted)`}
									</span>
								)}
							</div>
						))}
						{hasMore && (
							<div className="text-[13px] text-content-secondary">
								More messages available
							</div>
						)}
					</div>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};

/**
 * The messages come from the result; while the read runs, the card shows
 * only its running label.
 */
export const ForkChatTranscriptToolRenderer: React.FC<
	ForkToolRendererProps
> = ({ fallback: Fallback, ...props }) => {
	if (props.isError) {
		return (
			<ForkChatTranscriptTool
				messages={[]}
				hasMore={false}
				status={props.status}
				isError
				errorMessage={forkToolErrorMessage(props.result)}
			/>
		);
	}
	const resultRecord = parseArgs(props.result);
	if (!resultRecord) {
		if (props.status === "running") {
			return (
				<ForkChatTranscriptTool
					messages={[]}
					hasMore={false}
					status={props.status}
					isError={false}
				/>
			);
		}
		return <Fallback {...props} />;
	}
	const chatId = asString(resultRecord.chat_id);
	const messages = parseArray(resultRecord.messages, parseTranscriptMessage);
	if (chatId === "" || messages === null) {
		return <Fallback {...props} />;
	}
	return (
		<ForkChatTranscriptTool
			chatId={chatId}
			title={asString(resultRecord.title)}
			messages={messages}
			hasMore={resultRecord.has_more === true}
			status={props.status}
			isError={false}
		/>
	);
};
