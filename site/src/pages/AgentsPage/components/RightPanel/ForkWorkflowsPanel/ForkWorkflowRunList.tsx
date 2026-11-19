// This file is fork code: the run list of the Workflows tab
// (fork-workflows experiment).

import type React from "react";
import { createContext, useContext, useEffect, useRef, useState } from "react";
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
	/**
	 * Present only while more pages exist, so while it is absent the user
	 * cannot load another page: either no page has loaded yet or every
	 * loaded page is fetched through the end.
	 */
	onLoadOlderRuns?: () => void;
};

type ForkWorkflowRunRowProps = {
	run: WorkflowRun;
	onSelectRun: (runId: string) => void;
	buttonRef?: (button: HTMLButtonElement | null) => void;
};

// The shared slow clock of the list. A useTime on every row would give
// each loaded row its own interval, and the row count is unbounded, so
// terminal rows read this one value and only a running row keeps a
// one-second clock of its own.
const ListNowContext = createContext<number>(Date.now());

// The clock lives on the row so only rows with a running run tick every
// second; completed rows reuse the list's shared clock.
const ForkWorkflowRunRow: React.FC<ForkWorkflowRunRowProps> = ({
	run,
	onSelectRun,
	buttonRef,
}) => {
	const isRunning = run.status === "running";
	const sharedNow = useContext(ListNowContext);
	// The fast clock's interval runs only while the run is live, and
	// resetting it on each running render keeps the value fresh the
	// moment the interval turns on: a row that sat mounted while
	// interrupted can resume, and a stale capture would show the wrong
	// age until the first tick.
	const [runningNow, setRunningNow] = useState(() => Date.now());
	useEffect(() => {
		if (!isRunning) {
			return;
		}
		setRunningNow(Date.now());
		const handle = setInterval(() => setRunningNow(Date.now()), 1_000);
		return () => clearInterval(handle);
	}, [isRunning]);
	const now = isRunning ? runningNow : sharedNow;
	return (
		<button
			type="button"
			ref={buttonRef}
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
	// Ticks every 30 seconds, so the terminal rows' relative timestamps
	// stay fresh without one interval per row.
	const listNow = useTime(() => Date.now(), { interval: 30_000 });
	// The row buttons by run id, so focus restoration targets the actual
	// control and cannot land on a button a row grows later.
	const rowButtons = useRef(new Map<string, HTMLButtonElement>());
	// "Load older runs" holds focus while the last page loads. When that
	// page ends the paging, the button unmounts, so keep focus moving
	// through the runs instead of dropping it to the body.
	const hadLoadOlder = useRef(false);
	// Whether the button owned focus last, tracked through its own
	// events: the user may have moved focus away during the load, and
	// then the removal must not pull focus back.
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
			loadOlderHadFocus.current = false;
			const index = runs
				? runs.findIndex((run) => run.id === lastOlderRunId.current)
				: -1;
			// Focus continues on the row after the last one the user saw;
			// an empty last page leaves that row as the last one visible.
			// Without that run the row is unknown, so the list takes focus.
			const nextRunId =
				index >= 0 ? (runs?.[index + 1]?.id ?? runs?.[index]?.id) : undefined;
			const nextButton =
				(nextRunId && rowButtons.current.get(nextRunId)) || null;
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
			<ListNowContext.Provider value={listNow}>
				<ul
					ref={listRef}
					aria-label="Workflow runs"
					className="m-0 flex list-none flex-col gap-1 p-0 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link"
					// When "Load older runs" unmounts after the last page, focus
					// moves to the first run of the appended page, or to the last
					// run when that page is empty, or here, so it does not drop to
					// the body.
					tabIndex={-1}
				>
					{runs.map((run) => (
						<li key={run.id}>
							<ForkWorkflowRunRow
								run={run}
								onSelectRun={onSelectRun}
								buttonRef={(button) => {
									if (button) {
										rowButtons.current.set(run.id, button);
									} else {
										rowButtons.current.delete(run.id);
									}
								}}
							/>
						</li>
					))}
				</ul>
			</ListNowContext.Provider>
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
