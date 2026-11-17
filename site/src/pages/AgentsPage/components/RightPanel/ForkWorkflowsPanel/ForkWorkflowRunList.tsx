// This file is fork code: the run list of the Workflows tab
// (fork-workflows experiment).

import type React from "react";
import { useEffect, useRef } from "react";
import { getErrorMessage } from "#/api/errors";
import type { WorkflowRun } from "#/api/typesGenerated";
import { Alert } from "#/components/Alert/Alert";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
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
	runs: readonly WorkflowRun[] | undefined;
	error: unknown;
	onSelectRun: (runId: string) => void;
	hasOlderRuns?: boolean;
	isLoadingOlderRuns?: boolean;
	disabled?: boolean;
	/** Absent while the first page has not loaded. */
	onLoadOlderRuns?: () => void;
};

export const ForkWorkflowRunList: React.FC<ForkWorkflowRunListProps> = ({
	runs,
	error,
	onSelectRun,
	hasOlderRuns = false,
	isLoadingOlderRuns = false,
	disabled = false,
	onLoadOlderRuns,
}) => {
	const listRef = useRef<HTMLUListElement>(null);
	const hasRunning = runs?.some((run) => run.status === "running") ?? false;
	const now = useTime(() => Date.now(), {
		interval: hasRunning ? 1_000 : 30_000,
	});
	// "Load older runs" holds focus while the last page loads. When that
	// page ends the paging, the button unmounts, so keep focus on the list
	// instead of dropping it to the body.
	const hadLoadOlder = useRef(false);
	useEffect(() => {
		if (hasOlderRuns) {
			hadLoadOlder.current = true;
		} else if (
			hadLoadOlder.current &&
			document.activeElement === document.body &&
			listRef.current
		) {
			hadLoadOlder.current = false;
			listRef.current.focus({ preventScroll: true });
		}
	}, [hasOlderRuns]);

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
				ref={listRef}
				aria-label="Workflow runs"
				className="m-0 flex list-none flex-col gap-1 p-0 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link"
				// The list takes focus when "Load older runs" unmounts after
				// the last page, so keyboard focus does not drop to the body.
				tabIndex={-1}
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
									{run.title || run.script_preview || "Untitled run"}
								</span>
							</span>
							<span className="text-xs text-content-secondary">
								{[
									`Started ${timeFrom(run.created_at, now)}`,
									forkWorkflowRunDuration(run, now),
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
			{hasOlderRuns && onLoadOlderRuns && (
				<Button
					variant="subtle"
					size="sm"
					className="w-full"
					// aria-disabled instead of disabled keeps the control
					// focusable through both a background refetch and a
					// next-page fetch: browsers drop focus from a disabled
					// control, and a poll tick would steal it every 5s. The
					// guarded handler keeps the two fetches from overlapping.
					// The spinner stays decorative and the accessible name
					// stays "Load older runs": it is the same control, only
					// busy; the loading state is what aria-disabled signals.
					aria-disabled={disabled || isLoadingOlderRuns || undefined}
					onClick={disabled || isLoadingOlderRuns ? undefined : onLoadOlderRuns}
				>
					<Spinner size="sm" loading={isLoadingOlderRuns} />
					Load older runs
				</Button>
			)}
		</div>
	);
};
