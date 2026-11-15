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
): void => {
	window.dispatchEvent(
		new CustomEvent<ForkWorkflowResumeEventDetail>(FORK_WORKFLOW_RESUME_EVENT, {
			detail: { chatId, prompt: forkWorkflowResumePrompt(runId) },
		}),
	);
};

/** Reports whether a resume event targets this chat. */
export const forkWorkflowResumeEventForChat = (
	event: Event,
	chatId: string,
): string | undefined => {
	const detail = (event as CustomEvent<ForkWorkflowResumeEventDetail>).detail;
	return detail?.chatId === chatId ? detail.prompt : undefined;
};
