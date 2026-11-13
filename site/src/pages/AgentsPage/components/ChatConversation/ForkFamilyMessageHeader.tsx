// This file is fork code: it shows a message that message_family_chat, of the
// fork-family-messages experiment, delivered as one from its sender chat, so
// UserMessageContent.tsx, an upstream file, only calls useForkFamilyMessage.

import type React from "react";
import { ForkChatLink } from "../ChatElements/tools/ForkChatLink";
import {
	type FamilyMessage,
	parseFamilyMessage,
} from "../ChatElements/tools/forkFamilyMessage";
import { useForkExperiment } from "../ChatElements/tools/forkToolUtils";
import type { UserInlineRenderBlock } from "./messageHelpers";

const ForkFamilyMessageHeader: React.FC<{ message: FamilyMessage }> = ({
	message,
}) => (
	<div className="flex flex-wrap items-center gap-1 text-xs text-content-secondary">
		<span>Message from agent</span>
		<ForkChatLink chatId={message.fromChatId}>
			{message.fromTitle || "Untitled"}
		</ForkChatLink>
	</div>
);

/**
 * Returns the sender header of a family message and its blocks without the
 * envelope, or no header and the blocks unchanged for any other message.
 */
export const useForkFamilyMessage = (
	blocks: readonly UserInlineRenderBlock[],
): { header: React.ReactNode; blocks: readonly UserInlineRenderBlock[] } => {
	const isEnabled = useForkExperiment("fork-family-messages");
	const [firstBlock, ...restBlocks] = blocks;
	if (!isEnabled || firstBlock?.type !== "response") {
		return { header: null, blocks };
	}
	const message = parseFamilyMessage(firstBlock.text);
	if (!message) {
		return { header: null, blocks };
	}
	return {
		header: <ForkFamilyMessageHeader message={message} />,
		blocks: [{ ...firstBlock, text: message.body }, ...restBlocks],
	};
};
