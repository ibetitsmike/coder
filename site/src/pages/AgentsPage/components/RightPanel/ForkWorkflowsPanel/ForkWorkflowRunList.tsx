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

type ForkWorkflowRunRowProps = {
	run: WorkflowRun;
	onSelectRun: (runId: string) => void;
};

// The clock lives on the row so only rows with a running run tick every
// second; completed rows keep the slow interval.
const ForkWorkflowRunRow: React.FC<ForkWorkflowRunRowProps> = ({
	run,
	onSelectRun,
}) => {
	const now = useTime(() => Date.now(), {
		interval: run.status === "running" ? 1_000 : 30_000,
	});
	return (
		<button
			type="button"
			className="flex w-full cursor-pointer flex-col gap-1 rounded-md border border-solid border-border bg-transparent p-2 text-left hover:bg-surface-secondary"
			onClick={() => onSelectRun(run.id)}
		>
			<span className="flex w-full min-w-0 items-center gap-1.5">
				<ForkWorkflowStatusBadge {...forkWorkflowRunStatusMeta(run.status)} />
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
					run.step_counts.failed > 0 && `${run.step_counts.failed} failed`,
				]
					.filter(Boolean)
					.join(" · ")}
			</span>
		</button>
	);
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
	// "Load older runs" holds focus while the last page loads. When that
	// page ends the paging, the button unmounts, so keep focus moving
	// through the runs instead of dropping it to the body.
	const hadLoadOlder = useRef(false);
	// Whether the button owned focus last, tracked through its own events:
	// the user may have moved focus away during the load, and then the
	// removal must not pull it back onto the list.
	const loadOlderHadFocus = useRef(false);
	// The last run loaded while more pages remained. The next page's first
	// run follows it, so focus continues from where the user was.
	const lastOlderRunId = useRef<string>(null);
	useEffect(() => {
		if (hasOlderRuns) {
			hadLoadOlder.current = true;
			const last = runs?.[runs.length - 1];
			lastOlderRunId.current = last ? last.id : null;
		} else if (
			hadLoadOlder.current &&
			loadOlderHadFocus.current &&
			listRef.current
		) {
			hadLoadOlder.current = false;
			const rows = listRef.current.children;
			const index = runs
				? runs.findIndex((run) => run.id === lastOlderRunId.current)
				: -1;
			const nextRow = index >= 0 ? rows[index + 1] : undefined;
			const nextButton =
				nextRow instanceof HTMLElement ? nextRow.querySelector("button") : null;
			if (nextButton) {
				nextButton.focus({ preventScroll: true });
			} else {
				listRef.current.focus({ preventScroll: true });
			}
		}
	}, [hasOlderRuns, runs]);

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
				// When "Load older runs" unmounts after the last page, focus
				// moves to the first newer run, or here when there is no row
				// to take it, so it does not drop to the body.
				tabIndex={-1}
			>
				{runs.map((run) => (
					<li key={run.id}>
						<ForkWorkflowRunRow run={run} onSelectRun={onSelectRun} />
					</li>
				))}
			</ul>
			{hasOlderRuns && onLoadOlderRuns && (
				<Button
					variant="subtle"
					size="sm"
					className="w-full"
					// aria-disabled instead of disabled, because a native
					// disabled would drop keyboard focus during polling.
					aria-disabled={disabled || isLoadingOlderRuns || undefined}
					onClick={disabled || isLoadingOlderRuns ? undefined : onLoadOlderRuns}
					onFocus={() => {
						loadOlderHadFocus.current = true;
					}}
					// Any real blur hands ownership to whatever gained focus.
					// This relies on React not dispatching the blur a browser
					// fires when a focused element is removed during commit;
					// jsdom fires none.
					onBlur={() => {
						loadOlderHadFocus.current = false;
					}}
				>
					<Spinner size="sm" loading={isLoadingOlderRuns} aria-hidden />
					Load older runs
				</Button>
			)}
		</div>
	);
};
