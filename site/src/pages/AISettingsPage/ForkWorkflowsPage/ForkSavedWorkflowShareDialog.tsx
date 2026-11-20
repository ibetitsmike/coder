// This file is fork code: the confirmation that moves a personal saved
// workflow to the organization (fork-workflows experiment).

import { getErrorDetail, getErrorMessage } from "#/api/errors";
import type { ForkSavedWorkflowMetadata } from "#/api/typesGenerated";
import { Alert, AlertDescription } from "#/components/Alert/Alert";
import { ConfirmDialog } from "#/components/Dialog/ConfirmDialog/ConfirmDialog";

/**
 * The open share confirmation. While isSharing, onClose should keep the
 * dialog open, because a failure is reported only as error in this dialog.
 */
export type ForkSavedWorkflowShareState = {
	workflow: ForkSavedWorkflowMetadata;
	error: unknown;
	isSharing: boolean;
	onConfirm: () => void;
	onClose: () => void;
};

type ForkSavedWorkflowShareDialogProps = {
	state: ForkSavedWorkflowShareState;
	onCloseAutoFocus?: (event: Event) => void;
};

/**
 * Confirms moving a personal workflow to the organization. It shows the
 * error of a failed attempt and keeps its Share button busy while
 * state.isSharing.
 */
export const ForkSavedWorkflowShareDialog: React.FC<
	ForkSavedWorkflowShareDialogProps
> = ({ state, onCloseAutoFocus }) => (
	<ConfirmDialog
		type="info"
		hideCancel={false}
		open
		onClose={state.onClose}
		onCloseAutoFocus={onCloseAutoFocus}
		title={`Share ${state.workflow.name} with the organization?`}
		confirmText="Share"
		description={
			<>
				<p className="m-0">
					The workflow moves from your personal workflows to the organization
					workflows. Every member of the organization can read and run it. You
					can still edit and delete it, and an organization admin can change who
					has access.
				</p>
				{Boolean(state.error) && (
					<Alert severity="error" className="mt-3">
						<AlertDescription>
							{getErrorMessage(state.error, "Failed to share saved workflow.")}
							{getErrorDetail(state.error)
								? ` ${getErrorDetail(state.error)}`
								: ""}
						</AlertDescription>
					</Alert>
				)}
			</>
		}
		onConfirm={state.onConfirm}
		confirmLoading={state.isSharing}
	/>
);
