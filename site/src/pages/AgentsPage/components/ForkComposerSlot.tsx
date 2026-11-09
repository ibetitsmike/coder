// This file is fork code: it is the only place fork features add composer
// controls. Upstream's AgentChatInput.tsx renders ForkComposerSlot once, so
// fork features never edit that file and an upstream sync of it stays clean.

import type React from "react";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { ChatTodoPill } from "./ChatTodoPill";

type ForkComposerSlotProps = {
	chatId?: string;
	isDisabled: boolean;
};

/**
 * Renders fork composer controls at the start of the AgentChatInput controls
 * row. Each control renders only while its fork experiment is enabled.
 */
export const ForkComposerSlot: React.FC<ForkComposerSlotProps> = ({
	chatId,
}) => {
	const { experiments } = useDashboard();
	if (!chatId || !experiments.includes("fork-chat-todos")) {
		return null;
	}
	return <ChatTodoPill chatId={chatId} />;
};
