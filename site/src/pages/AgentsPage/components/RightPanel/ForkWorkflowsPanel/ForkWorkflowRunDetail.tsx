// This file is fork code: one run in the Workflows tab
// (fork-workflows experiment).

import { ChevronLeftIcon } from "lucide-react";
import type React from "react";
import { useQuery } from "react-query";
import { getErrorMessage } from "#/api/errors";
import type { ChatWorkflowRunDetail } from "#/api/forkWorkflows";
import { chatWorkflowRun } from "#/api/queries/forkWorkflows";
import { Alert } from "#/components/Alert/Alert";
import { Button } from "#/components/Button/Button";
import { Spinner } from "#/components/Spinner/Spinner";
import { useTime } from "#/hooks/useTime";
import { timeFrom } from "#/utils/time";
import { ForkWorkflowLongText } from "../../ForkWorkflows/ForkWorkflowLongText";
import { ForkWorkflowStatusBadge } from "../../ForkWorkflows/ForkWorkflowStatusBadge";
import { ForkWorkflowSteps } from "../../ForkWorkflows/ForkWorkflowSteps";
import {
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

	let content: React.ReactNode;
	if (runQuery.data) {
		content = (
			<ForkWorkflowRunDetailView run={runQuery.data} error={runQuery.error} />
		);
	} else if (runQuery.error) {
		content = (
			<Alert severity="error" prominent>
				<p className="text-sm text-content-primary">
					{getErrorMessage(runQuery.error, "Unable to load the workflow run.")}
				</p>
			</Alert>
		);
	} else {
		content = (
			<div className="flex items-center gap-2 text-sm text-content-secondary">
				<Spinner size="sm" loading />
				Loading workflow run...
			</div>
		);
	}

	return (
		<div className="flex min-w-0 flex-col gap-2 p-4">
			<Button variant="subtle" size="sm" className="w-fit px-1" onClick={onBack}>
				<ChevronLeftIcon />
				All runs
			</Button>
			{content}
		</div>
	);
};

type ForkWorkflowRunDetailViewProps = {
	run: ChatWorkflowRunDetail;
	error: unknown;
};

export const ForkWorkflowRunDetailView: React.FC<
	ForkWorkflowRunDetailViewProps
> = ({ run, error }) => {
	const isRunning = run.status === "running";
	const now = useTime(() => Date.now(), { disabled: !isRunning });
	const steps = run.steps
		.toSorted((left, right) => left.ordinal - right.ordinal)
		.map((step) => ({
			chatId: step.id,
			title: step.title,
			status: step.status,
			reused: step.reused,
			error: step.error,
		}));

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
				<h3 className="m-0 min-w-0 truncate text-sm font-medium text-content-primary">
					{run.title || "Untitled run"}
				</h3>
			</div>
			<div className="mt-1 text-xs text-content-secondary">
				{[
					`Started ${timeFrom(run.created_at, now)}`,
					forkWorkflowRunDuration(run.created_at, run.finished_at, now),
					forkWorkflowStepProgress(steps),
				].join(" · ")}
			</div>
			<div className="mt-1 font-mono text-xs text-content-secondary">
				{`Run ${run.id}`}
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
			{run.result !== null && (
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
