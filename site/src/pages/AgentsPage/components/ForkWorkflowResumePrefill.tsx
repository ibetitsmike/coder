// This file is fork code: the composer side of the workflow Resume button
// (fork-workflows experiment). The card and the panel dispatch a resume
// request; this listener lands it in the composer of its own chat only.

import type React from "react";
import { useEffect, useEffectEvent } from "react";
import { useQuery } from "react-query";
import { useParams } from "react-router";
import { chat } from "#/api/queries/chats";
import { useForkExperiment } from "./ChatElements/tools/forkToolUtils";
import type { ChatMessageInputRef } from "./ChatMessageInput/ChatMessageInput";
import {
	FORK_WORKFLOW_RESUME_EVENT,
	forkWorkflowResumeEventForChat,
} from "./ForkWorkflows/forkWorkflowResume";

type ForkWorkflowResumePrefillProps = {
	/** The composer handle of the chat this listener serves. */
	inputRef: React.RefObject<ChatMessageInputRef | null>;
};

/**
 * Applies resume requests from the workflow card and panel to the composer.
 * Existing text is kept: the request joins it on a new line. The composer is
 * focused, and the user sends the message, so the agent resumes in its turn.
 */
export const ForkWorkflowResumePrefill: React.FC<
	ForkWorkflowResumePrefillProps
> = ({ inputRef }) => {
	const isEnabled = useForkExperiment("fork-workflows");
	const { agentId } = useParams();
	// The chat page loads the chat; the listener only reads its cached copy
	// to ignore child chats, which do not resume runs themselves.
	const currentChat = useQuery({ ...chat(agentId ?? ""), enabled: false }).data;
	if (!isEnabled || !agentId || currentChat?.parent_chat_id !== undefined) {
		return null;
	}
	return (
		<ForkWorkflowResumePrefillListener chatId={agentId} inputRef={inputRef} />
	);
};

const ForkWorkflowResumePrefillListener: React.FC<{
	chatId: string;
	inputRef: React.RefObject<ChatMessageInputRef | null>;
}> = ({ chatId, inputRef }) => {
	const handleResume = useEffectEvent((event: Event) => {
		const prompt = forkWorkflowResumeEventForChat(event, chatId);
		if (prompt === undefined) {
			return;
		}
		const input = inputRef.current;
		if (!input) {
			return;
		}
		// restoreEditorState keeps the draft's chips (file references and
		// skill invocations), which a plain-text append would drop. An empty
		// editor state restores nothing, so a fresh composer gets the prompt
		// through setValue.
		const editorState = input.getEditorState();
		if (editorState && editorState !== "null") {
			input.restoreEditorState(editorState, `\n\n${prompt}`);
		} else {
			input.setValue(prompt);
		}
		input.focus();
	});

	// Scoped by chat: the event carries the target chat, and a listener of
	// another open chat drops requests that are not its own. handleResume is
	// an effect event, which stays out of the dependency array.
	useEffect(() => {
		const listener = (event: Event) => handleResume(event);
		window.addEventListener(FORK_WORKFLOW_RESUME_EVENT, listener);
		return () =>
			window.removeEventListener(FORK_WORKFLOW_RESUME_EVENT, listener);
	}, []);

	return null;
};
