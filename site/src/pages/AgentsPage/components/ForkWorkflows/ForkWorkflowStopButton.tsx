// This file is fork code: the Stop control of a workflow run
// (fork-workflows experiment). The live tool card and the Workflows panel
// run detail stop runs through this confirm dialog, mutation and cache
// updates.

import { SquareIcon } from "lucide-react";
import type React from "react";
import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage, getErrorStatus } from "#/api/errors";
import { stopWorkflowRun } from "#/api/queries/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { ConfirmDialog } from "#/components/Dialog/ConfirmDialog/ConfirmDialog";
import { toast } from "#/components/Toaster/toast";

type ForkWorkflowStopButtonProps = {
	run: WorkflowRunDetail;
	/** Receives focus when the dialog closes after the Stop button unmounted. */
	fallbackFocusRef: React.RefObject<HTMLElement | null>;
};

export const ForkWorkflowStopButton: React.FC<ForkWorkflowStopButtonProps> = ({
	run,
	fallbackFocusRef,
}) => {
	const [isConfirmOpen, setIsConfirmOpen] = useState(false);
	const stopButtonRef = useRef<HTMLButtonElement>(null);
	const queryClient = useQueryClient();
	const stopMutation = useMutation(
		stopWorkflowRun(queryClient, run.chat_id, run.id),
	);
	// Resetting the state, rather than hiding the dialog, keeps a resumed run
	// from reopening it.
	if (isConfirmOpen && run.status !== "running" && !stopMutation.isPending) {
		setIsConfirmOpen(false);
	}

	const handleStop = () => {
		stopMutation.mutate(undefined, {
			onError: (error) => {
				// A 409 means the run ended between the confirm and the
				// request, which is the outcome the user asked for, not a
				// failure. The refetch in the mutation brings the card and
				// the panel to the ended state.
				if (getErrorStatus(error) === 409) {
					toast.info("The workflow run already ended.");
				} else {
					toast.error(
						getErrorMessage(error, "Failed to stop the workflow run."),
						{ description: getErrorDetail(error) },
					);
				}
			},
			onSettled: () => setIsConfirmOpen(false),
		});
	};

	return (
		<>
			{run.status === "running" && (
				<Button
					ref={stopButtonRef}
					variant="outline"
					size="sm"
					aria-label="Stop workflow run"
					onClick={() => setIsConfirmOpen(true)}
				>
					<SquareIcon />
					Stop
				</Button>
			)}
			<ConfirmDialog
				open={isConfirmOpen}
				onClose={() => {
					if (!stopMutation.isPending) {
						setIsConfirmOpen(false);
					}
				}}
				onConfirm={handleStop}
				// The Stop button unmounts once the run ends, including on a
				// successful stop, so focus falls back to the run heading.
				onCloseAutoFocus={(event) => {
					event.preventDefault();
					(stopButtonRef.current ?? fallbackFocusRef.current)?.focus();
				}}
				title="Stop workflow run"
				description="The script stops, and steps that are still running fail and stop their child chats. This cannot be undone."
				confirmText="Stop run"
				confirmLoading={stopMutation.isPending}
				type="delete"
			/>
		</>
	);
};
