// This file is fork code: it is the only place fork features add composer
// controls. Upstream's AgentChatInput.tsx renders ForkComposerSlot once and
// only passes composer state to it, so an upstream sync of that file stays
// clean.

import type React from "react";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import type { SkillMetadata } from "./ChatMessageInput/SkillsTriggerMenu";
import { ChatTodoPill } from "./ChatTodoPill";
import { ForkChatWakeupsPill } from "./ForkChatWakeupsPill";
import { ForkPinnedSkillsPill } from "./ForkPinnedSkillsPill";
import { ForkPluginPacksPill } from "./ForkPluginPacksPill";

type ForkComposerSlotProps = {
	chatId?: string;
	isDisabled: boolean;
	workspaceSkills?: readonly SkillMetadata[];
};

/**
 * Renders fork composer controls at the start of the AgentChatInput controls
 * row. Each control renders only while its fork experiment is enabled.
 */
export const ForkComposerSlot: React.FC<ForkComposerSlotProps> = ({
	chatId,
	isDisabled,
	workspaceSkills,
}) => {
	const { experiments } = useDashboard();
	if (!chatId) {
		return null;
	}
	return (
		<>
			{experiments.includes("fork-chat-todos") && (
				<ChatTodoPill chatId={chatId} />
			)}
			{experiments.includes("fork-chat-wakeups") && (
				<ForkChatWakeupsPill chatId={chatId} isDisabled={isDisabled} />
			)}
			{experiments.includes("fork-plugin-packs") && (
				<ForkPluginPacksPill chatId={chatId} />
			)}
			{experiments.includes("fork-pinned-skills") && (
				<ForkPinnedSkillsPill
					chatId={chatId}
					workspaceSkills={workspaceSkills}
				/>
			)}
		</>
	);
};
