// This file is fork code: it is the only place fork features add composer
// controls. Upstream's AgentChatInput.tsx renders ForkComposerSlot once and
// only forwards values it already has in scope, so an upstream sync stays
// clean.

import type React from "react";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import type { ChatMessageInputRef } from "./ChatMessageInput/ChatMessageInput";
import type { SkillMetadata } from "./ChatMessageInput/SkillsTriggerMenu";
import { ChatTodoPill } from "./ChatTodoPill";
import { ForkChatWakeupsPill } from "./ForkChatWakeupsPill";
import { ForkPinnedSkillsPill } from "./ForkPinnedSkillsPill";
import { ForkPluginPacksPill } from "./ForkPluginPacksPill";
import { ForkWorkflowResumePrefill } from "./ForkWorkflowResumePrefill";

type ForkComposerSlotProps = {
	chatId?: string;
	isDisabled: boolean;
	workspaceSkills: readonly SkillMetadata[] | undefined;
	/**
	 * The composer handle, for fork features that write into the composer.
	 * Only read when a feature that needs it is enabled.
	 */
	inputRef?: React.RefObject<ChatMessageInputRef | null>;
};

/**
 * Renders fork composer controls at the start of the AgentChatInput controls
 * row. Each control renders only while its fork experiment is enabled.
 */
export const ForkComposerSlot: React.FC<ForkComposerSlotProps> = ({
	chatId,
	isDisabled,
	workspaceSkills,
	inputRef,
}) => {
	const { experiments } = useDashboard();
	if (!chatId) {
		return null;
	}
	return (
		<>
			{experiments.includes("fork-workflows") && inputRef && (
				<ForkWorkflowResumePrefill inputRef={inputRef} />
			)}
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
