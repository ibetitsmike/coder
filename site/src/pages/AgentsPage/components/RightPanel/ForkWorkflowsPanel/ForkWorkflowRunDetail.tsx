// This file is fork code: one run in the Workflows tab
// (fork-workflows experiment).

import { ChevronLeftIcon, SquareIcon } from "lucide-react";
import type React from "react";
import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import { chatWorkflowRun, stopWorkflowRun } from "#/api/queries/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { Alert } from "#/components/Alert/Alert";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import { ConfirmDialog } from "#/components/Dialog/ConfirmDialog/ConfirmDialog";
import { Spinner } from "#/components/Spinner/Spinner";
import { toast } from "#/components/Toaster/toast";
import { useTime } from "#/hooks/useTime";
import { timeFrom } from "#/utils/time";
import { ForkWorkflowLongText } from "../../ForkWorkflows/ForkWorkflowLongText";
import { ForkWorkflowStatusBadge } from "../../ForkWorkflows/ForkWorkflowStatusBadge";
import { ForkWorkflowSteps } from "../../ForkWorkflows/ForkWorkflowSteps";
import {
	forkWorkflowRunDetailSteps,
	forkWorkflowRunDuration,
	forkWorkflowRunStatusMeta,
	forkWorkflowStepProgress,
	formatForkWorkflowValue,
} from "../../ForkWorkflows/forkWorkflows";

type ForkWorkflowRunDetailProps = {
	chatId: string;
	runId: string;
	/** Polls a running run only while the tab is on screen. */
	isVisible: boolean;
	onBack: () => void;
};

export const ForkWorkflowRunDetail: React.FC<ForkWorkflowRunDetailProps> = ({
	chatId,
	runId,
	isVisible,
	onBack,
}) => {
	const runOptions = chatWorkflowRun(chatId, runId);
	const runQuery = useQuery({
		...runOptions,
		refetchInterval: isVisible ? runOptions.refetchInterval : false,
	});
	return (
		<ForkWorkflowRunDetailView
			run={runQuery.data}
			error={runQuery.error}
			onBack={onBack}
		/>
	);
};

type ForkWorkflowRunDetailViewProps = {
	run: WorkflowRunDetail | undefined;
	error: unknown;
	onBack: () => void;
};

export const ForkWorkflowRunDetailView: React.FC<
	ForkWorkflowRunDetailViewProps
> = ({ run, error, onBack }) => {
	let content: React.ReactNode;
	if (run) {
		content = <ForkWorkflowRunSummary run={run} error={error} />;
	} else if (error) {
		content = <ErrorAlert error={error} showDebugDetail={false} />;
	} else {
		content = (
			<div className="flex items-center gap-2 text-sm text-content-secondary">
				<Spinner size="sm" loading />
				Loading workflow run...
			</div>
		);
	}
	return (
		<div className="flex flex-col gap-2 p-4">
			<Button
				variant="subtle"
				size="sm"
				className="w-fit px-1"
				onClick={onBack}
			>
				<ChevronLeftIcon />
				All runs
			</Button>
			{content}
		</div>
	);
};

type ForkWorkflowRunSummaryProps = {
	run: WorkflowRunDetail;
	error: unknown;
};

const ForkWorkflowRunSummary: React.FC<ForkWorkflowRunSummaryProps> = ({
	run,
	error,
}) => {
	const isRunning = run.status === "running";
	const now = useTime(() => Date.now(), { disabled: !isRunning });
	const steps = forkWorkflowRunDetailSteps(run);

	return (
		<section aria-label="Workflow run" className="flex min-w-0 flex-col">
			{error !== null && (
				<Alert severity="warning">
					<p className="text-sm text-content-primary">
						{getErrorMessage(
							error,
							"Unable to refresh the workflow run. Showing cached data.",
						)}
					</p>
				</Alert>
			)}
			<div className="flex min-w-0 items-center gap-1.5">
				<ForkWorkflowStatusBadge {...forkWorkflowRunStatusMeta(run.status)} />
				<h3 className="m-0 min-w-0 flex-1 truncate text-sm font-medium text-content-primary">
					{run.title || run.script.slice(0, 200) || "Untitled run"}
				</h3>
				<ForkWorkflowRunStopButton run={run} />
			</div>
			<div className="mt-1 text-xs text-content-secondary">
				{[
					`Started ${timeFrom(run.created_at, now)}`,
					forkWorkflowRunDuration(run, now),
					forkWorkflowStepProgress(steps),
				].join(" · ")}
			</div>
			<div className="mt-1 font-mono text-xs text-content-secondary">
				Run {run.id}
			</div>
			{run.status === "interrupted" && (
				<p className="m-0 mt-1.5 text-xs text-content-secondary">
					The server restarted during this run. Resume it by asking the agent.
				</p>
			)}
			{run.error !== "" && (
				<ForkWorkflowLongText label="Error" text={run.error} isError />
			)}
			{steps.length > 0 && <ForkWorkflowSteps steps={steps} />}
			{run.result !== undefined && (
				<ForkWorkflowLongText
					label="Result"
					text={formatForkWorkflowValue(run.result)}
				/>
			)}
			{isRunning ? (
				<p className="m-0 mt-1.5 text-xs text-content-secondary">
					Logs appear when the run ends.
				</p>
			) : (
				run.logs.length > 0 && (
					<ForkWorkflowLongText label="Logs" text={run.logs.join("\n")} />
				)
			)}
			<ForkWorkflowLongText label="Script" text={run.script} />
		</section>
	);
};

type ForkWorkflowRunStopButtonProps = {
	run: WorkflowRunDetail;
};

const ForkWorkflowRunStopButton: React.FC<ForkWorkflowRunStopButtonProps> = ({
	run,
}) => {
	const [isConfirmOpen, setIsConfirmOpen] = useState(false);
	const stopButtonRef = useRef<HTMLButtonElement>(null);
	const queryClient = useQueryClient();
	const stopMutation = useMutation(
		stopWorkflowRun(queryClient, run.chat_id, run.id),
	);

	const handleStop = () => {
		stopMutation.mutate(undefined, {
			onError: (error) => {
				toast.error(
					getErrorMessage(error, "Failed to stop the workflow run."),
					{ description: getErrorDetail(error) },
				);
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
					onClick={() => setIsConfirmOpen(true)}
				>
					<SquareIcon />
					Stop
				</Button>
			)}
			<ConfirmDialog
				open={isConfirmOpen}
				onClose={() => setIsConfirmOpen(false)}
				onConfirm={handleStop}
				// The button unmounts once the run ends, and the dialog stays
				// mounted so a run that ends while it is open does not drop focus.
				onCloseAutoFocus={(event) => {
					if (stopButtonRef.current) {
						event.preventDefault();
						stopButtonRef.current.focus();
					}
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
