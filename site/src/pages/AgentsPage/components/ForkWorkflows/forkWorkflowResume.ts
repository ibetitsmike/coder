// This file is fork code: the resume request of a workflow run
// (fork-workflows experiment), shared by the live tool card and the
// Workflows panel.

/** The message the Resume button puts into the composer for the user to send. */
export const forkWorkflowResumePrompt = (runId: string): string =>
	`Resume workflow run ${runId}`;

export const FORK_WORKFLOW_RESUME_EVENT = "fork-workflow-resume";

type ForkWorkflowResumeEventDetail = {
	chatId: string;
	prompt: string;
};

/** Prefills the chat's composer with a resume request for one run. */
export const requestForkWorkflowResume = (
	chatId: string,
	runId: string,
	{ skipIfPrompted = false }: { skipIfPrompted?: boolean } = {},
): void => {
	const prompt = forkWorkflowResumePrompt(runId);
	if (
		skipIfPrompted &&
		document.body.dataset.forkWorkflowResumePrompted === prompt
	) {
		return;
	}
	document.body.dataset.forkWorkflowResumePrompted = prompt;
	window.dispatchEvent(
		new CustomEvent<ForkWorkflowResumeEventDetail>(FORK_WORKFLOW_RESUME_EVENT, {
			detail: { chatId, prompt },
		}),
	);
};

/**
 * Reports the resume prompt a resume event carries when it targets this
 * chat. The event comes from this page's own buttons, but any other event
 * on the same name returns undefined instead of crashing.
 */
export const forkWorkflowResumeEventForChat = (
	event: Event,
	chatId: string,
): string | undefined => {
	if (!(event instanceof CustomEvent)) {
		return undefined;
	}
	const { chatId: eventChatId, prompt } = event.detail ?? {};
	return eventChatId === chatId && typeof prompt === "string"
		? prompt
		: undefined;
};
