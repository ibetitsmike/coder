// This file is fork code: it is the only place fork features add composer
// controls. Upstream's AgentChatInput.tsx renders ForkComposerSlot once, so
// fork features never edit that file and an upstream sync of it stays clean.

import type React from "react";

type ForkComposerSlotProps = {
	chatId?: string;
	isDisabled: boolean;
};

/**
 * Renders fork composer controls at the start of the AgentChatInput controls
 * row. It renders nothing until a fork feature adds a control.
 */
export const ForkComposerSlot: React.FC<ForkComposerSlotProps> = () => null;
