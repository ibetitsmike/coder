// This file is fork code: the Resume control of an interrupted workflow run
// (fork-workflows experiment), shared by the live tool card and the
// Workflows panel. The agent resumes with run_workflow and resume_run_id, so
// the button only puts a prefilled request into the composer for the user to
// send.

import { PlayIcon } from "lucide-react";
import type React from "react";
import { Button } from "#/components/Button/Button";
import { requestForkWorkflowResume } from "./forkWorkflowResume";

type ForkWorkflowResumeButtonProps = {
	/** The interrupted run to resume. */
	runId: string;
	/** The chat whose composer the request lands in. */
	chatId: string;
};

export const ForkWorkflowResumeButton: React.FC<
	ForkWorkflowResumeButtonProps
> = ({ runId, chatId }) => (
	<Button
		variant="outline"
		size="sm"
		onClick={() => requestForkWorkflowResume(chatId, runId)}
	>
		<PlayIcon />
		Resume run
	</Button>
);
