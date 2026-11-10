// This file is fork code: it renders the read_chat_transcript tool of the
// fork-chat-todos experiment.

import { ExternalLinkIcon } from "lucide-react";
import type React from "react";
import { Link, useLocation } from "react-router";
import { safeBuildAgentChatPath } from "../../../utils/navigation";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { ToolCall } from "./ToolCall";
import {
	asNumber,
	asRecord,
	asString,
	parseArgs,
	parseArray,
	type ToolStatus,
} from "./utils";

type TranscriptMessage = {
	role: string;
	preview: string;
	omittedParts: number;
};

const parseTranscriptMessage = (item: unknown): TranscriptMessage | null => {
	const record = asRecord(item);
	if (!record || typeof record.role !== "string") {
		return null;
	}
	const firstPart = Array.isArray(record.parts)
		? asRecord(record.parts[0])
		: null;
	const type = firstPart === null ? "" : asString(firstPart.type);
	const preview =
		type === "text" || type === "reasoning"
			? asString(firstPart?.text)
			: type === "tool-call" || type === "tool-result"
				? asString(firstPart?.tool_name)
				: type;
	return {
		role: record.role,
		preview,
		omittedParts: asNumber(record.omitted_parts) ?? 0,
	};
};

type ForkChatTranscriptToolProps = {
	chatId: string;
	title: string;
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
	const location = useLocation();
	let label: string;
	if (status === "running") {
		label = "Reading chat transcript…";
	} else if (isError) {
		label = "Failed to read chat transcript";
	} else {
		const noun = messages.length === 1 ? "message" : "messages";
		label = `Read ${messages.length} ${noun} from ${title || "Untitled"}`;
	}
	const errorDetail = errorMessage?.trim();
	const chatPath = chatId === "" ? null : safeBuildAgentChatPath({ chatId });

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || "Failed to read chat transcript"}
			hasContent={messages.length > 0 || chatId !== "" || Boolean(errorDetail)}
		>
			<ToolCall.Header iconName="fork_chat_transcript" label={label} />
			<ToolCall.Content>
				{errorDetail && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{chatId !== "" && (
					<div className="mt-1.5">
						{chatPath === null ? (
							<div className="text-[13px] text-content-secondary">
								{title || "Untitled"}
							</div>
						) : (
							<div className="flex items-center gap-1.5">
								<Link
									to={{
										pathname: chatPath,
										search: location.search,
									}}
									className="flex items-center gap-1.5 text-[13px] text-content-secondary opacity-50 transition-opacity hover:opacity-100"
								>
									{title || "Untitled"}
									<ExternalLinkIcon className="size-3 shrink-0" />
								</Link>
							</div>
						)}
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
										{`(+${message.omittedParts} parts omitted)`}
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
	const resultRecord = parseArgs(props.result);
	if (props.isError) {
		const errorMessage = resultRecord
			? asString(resultRecord.error || resultRecord.message)
			: asString(props.result);
		return (
			<ForkChatTranscriptTool
				chatId=""
				title=""
				messages={[]}
				hasMore={false}
				status={props.status}
				isError
				errorMessage={errorMessage}
			/>
		);
	}
	if (!resultRecord) {
		if (props.status === "running") {
			return (
				<ForkChatTranscriptTool
					chatId=""
					title=""
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
