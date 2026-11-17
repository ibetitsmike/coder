// This file is fork code: one run in the Workflows tab
// (fork-workflows experiment).

import { ChevronLeftIcon } from "lucide-react";
import type React from "react";
import { useRef } from "react";
import { useQuery } from "react-query";
import { getErrorMessage } from "#/api/errors";
import { chat } from "#/api/queries/chats";
import { chatWorkflowRun } from "#/api/queries/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { Alert } from "#/components/Alert/Alert";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import { Spinner } from "#/components/Spinner/Spinner";
import { useTime } from "#/hooks/useTime";
import { timeFrom } from "#/utils/time";
import { ForkWorkflowLongText } from "../../ForkWorkflows/ForkWorkflowLongText";
import { ForkWorkflowPhaseRail } from "../../ForkWorkflows/ForkWorkflowPhaseRail";
import { ForkWorkflowResumeButton } from "../../ForkWorkflows/ForkWorkflowResumeButton";
import { ForkWorkflowStatusBadge } from "../../ForkWorkflows/ForkWorkflowStatusBadge";
import { ForkWorkflowSteps } from "../../ForkWorkflows/ForkWorkflowSteps";
import { ForkWorkflowStopButton } from "../../ForkWorkflows/ForkWorkflowStopButton";
import {
	forkWorkflowRunDetailSteps,
	forkWorkflowRunDuration,
	forkWorkflowRunStatusMeta,
	forkWorkflowStepProgress,
	formatForkWorkflowValue,
} from "../../ForkWorkflows/forkWorkflows";
import { useCanWriteChat } from "../../ForkWorkflows/useCanWriteChat";

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
	// The panel loads no chat of its own, so the run's chat decides whether
	// the viewer may stop or resume; the query reads the chat page's cached
	// copy when it has one.
	const chatQuery = useQuery({ ...chat(run?.chat_id ?? ""), enabled: !!run });
	const canWriteChat = useCanWriteChat(chatQuery.data);
	// Plan mode turns omit run_workflow, so a resume could not run it.
	const isResumeAvailable = chatQuery.data?.plan_mode !== "plan";
	let content: React.ReactNode;
	if (run) {
		content = (
			<ForkWorkflowRunSummary
				run={run}
				error={error}
				canWriteChat={canWriteChat}
				isResumeAvailable={isResumeAvailable}
			/>
		);
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
	canWriteChat: boolean | undefined;
	/** Plan mode turns omit run_workflow, so Resume cannot work. */
	isResumeAvailable: boolean;
};

const ForkWorkflowRunSummary: React.FC<ForkWorkflowRunSummaryProps> = ({
	run,
	error,
	canWriteChat,
	isResumeAvailable,
}) => {
	const isRunning = run.status === "running";
	const now = useTime(() => Date.now(), { disabled: !isRunning });
	const steps = forkWorkflowRunDetailSteps(run);
	const headingRef = useRef<HTMLHeadingElement>(null);

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
				<h3
					ref={headingRef}
					tabIndex={-1}
					className="m-0 min-w-0 flex-1 truncate text-sm font-medium text-content-primary focus:outline-none"
				>
					{run.title || run.script.slice(0, 200) || "Untitled run"}
				</h3>
				{canWriteChat === true && (
					<>
						<ForkWorkflowStopButton run={run} fallbackFocusRef={headingRef} />
						{run.status === "interrupted" && isResumeAvailable && (
							<ForkWorkflowResumeButton runId={run.id} chatId={run.chat_id} />
						)}
					</>
				)}
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
			{run.status === "interrupted" && canWriteChat !== true && (
				<p className="m-0 mt-1.5 text-xs text-content-secondary">
					The server restarted during this run. Resume it by asking the agent.
				</p>
			)}
			{run.error !== "" && (
				<ForkWorkflowLongText label="Error" text={run.error} isError />
			)}
			{run.args !== undefined && (
				<ForkWorkflowLongText
					label="Arguments"
					text={formatForkWorkflowValue(run.args)}
				/>
			)}
			{steps.length > 0 && (
				<ForkWorkflowSteps
					steps={steps}
					phases={run.phases}
					currentPhase={run.current_phase}
					runStatus={run.status}
				/>
			)}
			{steps.length === 0 && run.phases.length > 0 && (
				<ForkWorkflowPhaseRail
					phases={run.phases}
					currentPhase={run.current_phase}
					steps={steps}
					runStatus={run.status}
				/>
			)}
			{run.result !== undefined && (
				<ForkWorkflowLongText
					label="Result"
					text={formatForkWorkflowValue(run.result)}
				/>
			)}
			{run.logs.length > 0 && (
				<ForkWorkflowLongText label="Logs" text={run.logs.join("\n")} />
			)}
			{run.logs.length === 0 && isRunning && (
				<p className="m-0 mt-1.5 text-xs text-content-secondary">
					Logs appear as the script prints them.
				</p>
			)}
			<ForkWorkflowLongText label="Script" text={run.script} />
		</section>
	);
};
