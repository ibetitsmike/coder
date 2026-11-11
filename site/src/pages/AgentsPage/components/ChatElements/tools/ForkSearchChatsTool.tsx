// This file is fork code: it renders the search_chats tool of the
// fork-chat-todos experiment.

import type React from "react";
import { ForkChatLink } from "./ForkChatLink";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import {
	asRecord,
	asString,
	parseArgs,
	parseArray,
	type ToolStatus,
} from "./utils";

const failedLabel = "Failed to search chats";

type SearchChatsEntry = {
	chatId: string;
	title: string;
	status: string;
	shared: boolean;
};

export const parseSearchChatsEntry = (
	item: unknown,
): SearchChatsEntry | null => {
	const record = asRecord(item);
	if (!record || typeof record.chat_id !== "string") {
		return null;
	}
	return {
		chatId: record.chat_id,
		title: asString(record.title),
		status: asString(record.status),
		shared: record.shared === true,
	};
};

type ForkSearchChatsToolProps = {
	query: string;
	chats: readonly SearchChatsEntry[];
	hasMore: boolean;
	status: ToolStatus;
	isError: boolean;
	errorMessage?: string;
};

const ForkSearchChatsTool: React.FC<ForkSearchChatsToolProps> = ({
	query,
	chats,
	hasMore,
	status,
	isError,
	errorMessage,
}) => {
	let label: string;
	if (status === "running") {
		label = "Searching chats…";
	} else if (isError) {
		label = failedLabel;
	} else if (chats.length === 0) {
		label = "No chats found";
	} else if (chats.length === 1) {
		label = "Found 1 chat";
	} else {
		label = `Found ${chats.length} chats`;
	}
	const errorDetail = errorMessage?.trim();

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || failedLabel}
			hasContent={chats.length > 0 || query !== "" || Boolean(errorDetail)}
		>
			<ToolCall.Header iconName="fork_search_chats" label={label} />
			<ToolCall.Content>
				{errorDetail && (
					<ToolCall.ErrorDetail>{errorDetail}</ToolCall.ErrorDetail>
				)}
				{query !== "" && (
					<div className="mt-1.5 break-words font-mono text-xs text-content-secondary">
						{query}
					</div>
				)}
				{chats.length > 0 && (
					<div className="mt-1.5">
						{chats.map((chat) => (
							<ForkChatLink key={chat.chatId} chatId={chat.chatId}>
								{`${chat.title || "Untitled"} (${chat.status}${chat.shared ? ", shared" : ""})`}
							</ForkChatLink>
						))}
						{hasMore && (
							<div className="text-[13px] text-content-secondary">
								More chats available
							</div>
						)}
					</div>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};

/**
 * The chat list comes from the result; while the search runs, the card
 * shows the streamed query from its args.
 */
export const ForkSearchChatsToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	const query = asString(parseArgs(props.args)?.query);
	if (props.isError) {
		return (
			<ForkSearchChatsTool
				query={query}
				chats={[]}
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
				<ForkSearchChatsTool
					query={query}
					chats={[]}
					hasMore={false}
					status={props.status}
					isError={false}
				/>
			);
		}
		return <Fallback {...props} />;
	}
	const chats = parseArray(resultRecord.chats, parseSearchChatsEntry);
	if (!chats) {
		return <Fallback {...props} />;
	}
	return (
		<ForkSearchChatsTool
			query={query}
			chats={chats}
			hasMore={resultRecord.has_more === true}
			status={props.status}
			isError={false}
		/>
	);
};
