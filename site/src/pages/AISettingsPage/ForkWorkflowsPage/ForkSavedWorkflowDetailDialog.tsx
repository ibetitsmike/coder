// This file is fork code: the read-only detail dialog of a saved workflow
// (fork-workflows experiment).

import { useRef } from "react";
import type { ForkSavedWorkflow } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "#/components/Dialog/Dialog";
import { Loader } from "#/components/Loader/Loader";
import { RetryButton } from "#/pages/AgentsPage/components/RetryButton";
import { SavedWorkflowTextArea } from "./ForkSavedWorkflowEditorDialog";
import { formatSavedWorkflowPhases } from "./savedWorkflowForm";

type ForkSavedWorkflowDetailDialogProps = {
	/** Unset while the workflow loads or after it failed to. */
	workflow?: ForkSavedWorkflow;
	isLoading: boolean;
	error: unknown;
	onRetry?: () => void;
	isRetrying: boolean;
	onClose: () => void;
	onCloseAutoFocus?: (event: Event) => void;
};

/** Shows a saved workflow's script, args schema, and phases read-only. */
export const ForkSavedWorkflowDetailDialog: React.FC<
	ForkSavedWorkflowDetailDialogProps
> = ({
	workflow,
	isLoading,
	error,
	onRetry,
	isRetrying,
	onClose,
	onCloseAutoFocus,
}) => {
	const closeButtonRef = useRef<HTMLButtonElement>(null);

	return (
		<Dialog open onOpenChange={(open) => !open && onClose()}>
			<DialogContent
				className="flex max-w-2xl flex-col gap-0 overflow-hidden p-0"
				onCloseAutoFocus={onCloseAutoFocus}
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					closeButtonRef.current?.focus();
				}}
			>
				<DialogHeader className="px-6 pt-6">
					<DialogTitle>
						{isLoading
							? "Loading saved workflow"
							: `View ${workflow?.name ?? "saved workflow"}`}
					</DialogTitle>
					<DialogDescription className="whitespace-pre-wrap">
						{workflow === undefined
							? null
							: workflow.description || "This workflow has no description."}
					</DialogDescription>
				</DialogHeader>
				<div className="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto px-6 py-4">
					{isLoading ? (
						<Loader />
					) : workflow ? (
						<>
							<SavedWorkflowTextArea
								label="Script"
								value={workflow.script}
								readOnly
								mono
								minRows={3}
							/>
							{workflow.args_schema && (
								<SavedWorkflowTextArea
									label="Args schema"
									value={JSON.stringify(workflow.args_schema, null, 2)}
									readOnly
									mono
									minRows={3}
								/>
							)}
							{workflow.phases.length > 0 && (
								<SavedWorkflowTextArea
									label="Phases"
									value={formatSavedWorkflowPhases(workflow.phases)}
									readOnly
									minRows={3}
								/>
							)}
						</>
					) : (
						<ErrorAlert error={error} showDebugDetail={false} />
					)}
				</div>
				{Boolean(error) && !isLoading && workflow && (
					<ErrorAlert
						className="mx-6 mb-4"
						error={error}
						showDebugDetail={false}
					/>
				)}
				<DialogFooter className="border-t px-6 py-4">
					{!isLoading && onRetry && (
						<RetryButton isRetrying={isRetrying} onRetry={onRetry} />
					)}
					<Button ref={closeButtonRef} variant="outline" onClick={onClose}>
						Close
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
};
