// This file is fork code: the run list of the Workflows tab
// (fork-workflows experiment).

import type React from "react";
import { getErrorMessage } from "#/api/errors";
import type { ChatWorkflowRun } from "#/api/forkWorkflows";
import { Alert } from "#/components/Alert/Alert";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Spinner } from "#/components/Spinner/Spinner";
import { useTime } from "#/hooks/useTime";
import { timeFrom } from "#/utils/time";
import { ForkWorkflowStatusBadge } from "../../ForkWorkflows/ForkWorkflowStatusBadge";
import {
	forkWorkflowRunDuration,
	forkWorkflowRunStatusMeta,
	forkWorkflowStepCountProgress,
} from "../../ForkWorkflows/forkWorkflows";

type ForkWorkflowRunListProps = {
	runs: readonly ChatWorkflowRun[] | undefined;
	error: unknown;
	onSelectRun: (runId: string) => void;
};

export const ForkWorkflowRunList: React.FC<ForkWorkflowRunListProps> = ({
	runs,
	error,
	onSelectRun,
}) => {
	const hasRunning = runs?.some((run) => run.status === "running") ?? false;
	const now = useTime(() => Date.now(), {
		interval: hasRunning ? 1_000 : 30_000,
	});

	if (!runs) {
		return error ? (
			<div className="p-4">
				<ErrorAlert error={error} showDebugDetail={false} />
			</div>
		) : (
			<div className="flex items-center gap-2 p-4 text-sm text-content-secondary">
				<Spinner size="sm" loading />
				Loading workflow runs...
			</div>
		);
	}
	return (
		<div className="flex flex-col gap-2 p-4">
			{error !== null && (
				<Alert severity="warning">
					<p className="text-sm text-content-primary">
						{getErrorMessage(
							error,
							"Unable to refresh workflow runs. Showing cached data.",
						)}
					</p>
				</Alert>
			)}
			<ul
				aria-label="Workflow runs"
				className="m-0 flex list-none flex-col gap-1 p-0"
			>
				{runs.map((run) => (
					<li key={run.id}>
						<button
							type="button"
							className="flex w-full cursor-pointer flex-col gap-1 rounded-md border border-solid border-border bg-transparent p-2 text-left hover:bg-surface-secondary"
							onClick={() => onSelectRun(run.id)}
						>
							<span className="flex w-full min-w-0 items-center gap-1.5">
								<ForkWorkflowStatusBadge
									{...forkWorkflowRunStatusMeta(run.status)}
								/>
								<span className="min-w-0 truncate text-sm text-content-primary">
									{run.title || `${run.script_preview}…`}
								</span>
							</span>
							<span className="text-xs text-content-secondary">
								{[
									`Started ${timeFrom(run.created_at, now)}`,
									forkWorkflowRunDuration(run.created_at, run.finished_at, now),
									forkWorkflowStepCountProgress(
										run.step_counts.done,
										run.step_counts.total,
									),
									run.step_counts.failed > 0 &&
										`${run.step_counts.failed} failed`,
								]
									.filter(Boolean)
									.join(" · ")}
							</span>
						</button>
					</li>
				))}
			</ul>
		</div>
	);
};
