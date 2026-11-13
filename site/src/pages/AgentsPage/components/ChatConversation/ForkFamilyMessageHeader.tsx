// This file is fork code: it shows a message that message_family_chat, of the
// fork-family-messages experiment, delivered as one from its sender chat, so
// UserMessageContent.tsx, an upstream file, only calls into it.

import type React from "react";
import { ForkChatLink } from "../ChatElements/tools/ForkChatLink";
import {
	type FamilyMessage,
	parseFamilyMessage,
} from "../ChatElements/tools/forkFamilyMessage";
import { useForkExperiment } from "../ChatElements/tools/forkToolUtils";
import type { UserInlineRenderBlock } from "./messageHelpers";

/** Names the chat that sent a family message and links to it. */
export const ForkFamilyMessageHeader: React.FC<{ message: FamilyMessage }> = ({
	message,
}) => (
	<div className="flex flex-wrap items-center gap-1 text-xs text-content-secondary">
		<span>Message from agent</span>
		<ForkChatLink chatId={message.fromChatId} className="text-xs opacity-100">
			{message.fromTitle}
		</ForkChatLink>
	</div>
);

type UserInlineContent = {
	isAsyncAnswer: boolean;
	blocks: readonly UserInlineRenderBlock[];
};

/**
 * Parses a family message from the first block when fork-family-messages is
 * on and returns its body blocks. Any other message goes to splitAsyncAnswer,
 * so a body that starts like an async answer is not labelled as one.
 */
export const useForkFamilyMessage = (
	blocks: readonly UserInlineRenderBlock[],
	splitAsyncAnswer: (
		blocks: readonly UserInlineRenderBlock[],
	) => UserInlineContent,
): UserInlineContent & { familyMessage: FamilyMessage | undefined } => {
	const isEnabled = useForkExperiment("fork-family-messages");
	const [firstBlock, ...restBlocks] = blocks;
	if (!isEnabled || firstBlock?.type !== "response") {
		return { ...splitAsyncAnswer(blocks), familyMessage: undefined };
	}
	const familyMessage = parseFamilyMessage(firstBlock.text);
	if (!familyMessage) {
		return { ...splitAsyncAnswer(blocks), familyMessage: undefined };
	}
	return {
		familyMessage,
		isAsyncAnswer: false,
		blocks: [{ ...firstBlock, text: familyMessage.body }, ...restBlocks],
	};
};
