// This file is fork code: it parses the Starlark workflow tool results of
// the fork-workflows experiment and maps their statuses to display values.

import type { WorkflowRun, WorkflowRunDetail } from "#/api/typesGenerated";
import { humanDurationShort } from "#/utils/time";
import { asRecord, asString } from "../ChatElements/runtimeTypeUtils";
import { parseArgs, parseArray } from "../ChatElements/tools/utils";

export type ForkWorkflowStep = {
	chatId: string;
	title: string;
	status: string;
	reused: boolean;
	error: string;
	phase: string;
};

/** A run as run_workflow and await_workflow report it. */
export type ForkWorkflowRunSnapshot = {
	runId: string;
	status: string;
	/** What main(args) returned; undefined until the run completes. */
	result: unknown;
	error: string;
	/**
	 * Phases in display order, and the phase the script set last. Both are
	 * empty for a run whose script uses no phases.
	 */
	phases: readonly string[];
	currentPhase: string;
	steps: readonly ForkWorkflowStep[];
	logs: readonly string[];
};

export type ForkWorkflowTone =
	| "running"
	| "success"
	| "warning"
	| "destructive"
	| "muted";

type StatusMeta = { label: string; tone: ForkWorkflowTone };

const runStatusMeta: Record<string, StatusMeta> = {
	running: { label: "Running", tone: "running" },
	completed: { label: "Completed", tone: "success" },
	failed: { label: "Failed", tone: "destructive" },
	// coderd restarted while the run was running.
	interrupted: { label: "Interrupted", tone: "warning" },
	stopped: { label: "Stopped", tone: "muted" },
};

const stepStatusMeta: Record<string, StatusMeta> = {
	running: { label: "Running", tone: "running" },
	// The step ended its turn without workflow_report and got one reminder.
	reminded: { label: "Reminded", tone: "warning" },
	reported: { label: "Reported", tone: "success" },
	failed: { label: "Failed", tone: "destructive" },
};

// A status that a newer server adds still renders, as a neutral badge.
const unknownStatusMeta = (status: string): StatusMeta => ({
	label: status ? status.charAt(0).toUpperCase() + status.slice(1) : "Unknown",
	tone: "muted",
});

export const forkWorkflowRunStatusMeta = (status: string): StatusMeta =>
	runStatusMeta[status] ?? unknownStatusMeta(status);

export const forkWorkflowStepStatusMeta = (status: string): StatusMeta =>
	stepStatusMeta[status] ?? unknownStatusMeta(status);

const parseStep = (value: unknown): ForkWorkflowStep | null => {
	const record = asRecord(value);
	const chatId = asString(record?.chat_id);
	if (!record || chatId === "") {
		return null;
	}
	return {
		chatId,
		title: asString(record.title),
		status: asString(record.status),
		reused: record.reused === true,
		error: asString(record.error),
		phase: asString(record.phase),
	};
};

/** Parses a run_workflow or await_workflow result, or returns null. */
export const parseForkWorkflowRunSnapshot = (
	value: unknown,
): ForkWorkflowRunSnapshot | null => {
	const record = parseArgs(value);
	const runId = asString(record?.run_id);
	const status = asString(record?.status);
	const steps = parseArray(record?.steps ?? [], parseStep);
	if (!record || runId === "" || status === "" || !steps) {
		return null;
	}
	const logs = parseArray(record.logs ?? [], (line) =>
		typeof line === "string" ? line : null,
	);
	// A phase name a newer server adds still renders, so a malformed entry
	// drops from the list instead of rejecting the run.
	const phases: string[] = [];
	if (Array.isArray(record?.phases)) {
		for (const name of record.phases) {
			const trimmed = typeof name === "string" ? name.trim() : "";
			if (trimmed !== "" && !phases.includes(trimmed)) {
				phases.push(trimmed);
			}
		}
	}
	return {
		runId,
		status,
		result: record.result,
		error: asString(record.error),
		phases,
		currentPhase: asString(record.current_phase),
		steps,
		logs: logs ?? [],
	};
};

/** Lists a run detail's steps in spawn order. */
export const forkWorkflowRunDetailSteps = (
	run: WorkflowRunDetail,
): ForkWorkflowStep[] =>
	run.steps
		.toSorted((left, right) => left.ordinal - right.ordinal)
		.map((step) => ({
			chatId: step.id,
			title: step.title,
			status: step.status,
			reused: step.reused,
			error: step.error,
			phase: step.phase,
		}));

/** Converts a run detail to the snapshot that a tool result parses to. */
const forkWorkflowRunDetailSnapshot = (
	run: WorkflowRunDetail,
): ForkWorkflowRunSnapshot => ({
	runId: run.id,
	status: run.status,
	result: run.result,
	error: run.error,
	phases: run.phases,
	currentPhase: run.current_phase,
	steps: forkWorkflowRunDetailSteps(run),
	logs: run.logs,
});

/**
 * Applies a run detail to a running tool result. The detail has no logs until
 * the run ends and does not mark reused steps, so those come from the result.
 */
export const mergeForkWorkflowRunDetail = (
	snapshot: ForkWorkflowRunSnapshot,
	run: WorkflowRunDetail,
): ForkWorkflowRunSnapshot => {
	const detail = forkWorkflowRunDetailSnapshot(run);
	// Phase lists only grow, so a polled detail whose list is a strict
	// prefix of the snapshot's lags the snapshot's last journal write, and
	// the merge keeps the snapshot's phase state. Any other detail is equal
	// or newer, and a detail that ends the run is the authority again.
	const detailLags =
		detail.phases.length < snapshot.phases.length &&
		detail.phases.every((name, i) => name === snapshot.phases[i]);
	const phaseState = runEnded(detail.status)
		? detail
		: detailLags
			? { phases: snapshot.phases, currentPhase: snapshot.currentPhase }
			: { phases: detail.phases, currentPhase: detail.currentPhase };
	const reusedChatIds = new Set(
		snapshot.steps.filter((step) => step.reused).map((step) => step.chatId),
	);
	return {
		...detail,
		...phaseState,
		steps: detail.steps.map((step) =>
			reusedChatIds.has(step.chatId) ? { ...step, reused: true } : step,
		),
		logs: detail.logs.length > 0 ? detail.logs : snapshot.logs,
	};
};

/** Summarizes how far a phase got, from its steps and the run status. */
export type ForkWorkflowPhaseState = "pending" | "running" | "done" | "failed";

/** Run statuses that cannot make progress anymore. */
const runEnded = (status: string): boolean =>
	status === "failed" || status === "stopped" || status === "completed";

export const forkWorkflowPhaseState = (
	steps: readonly ForkWorkflowStep[],
): ForkWorkflowPhaseState => {
	if (steps.length === 0) {
		return "pending";
	}
	if (steps.some((step) => step.status === "failed")) {
		return "failed";
	}
	return steps.every((step) => step.status === "reported") ? "done" : "running";
};

/**
 * The state of one phase of a run. A phase the script has not set or passed
 * stays pending, on any run status. A phase whose steps are all reported is
 * done, whatever the run went on to do. Only the phase the script was in
 * when a run failed or stopped shows as failed, and on a completed run
 * every reached phase is done: the script finished it or moved past it.
 */
export const forkWorkflowPhaseStateOf = (
	phase: string,
	index: number,
	phases: readonly string[],
	currentPhase: string,
	steps: readonly ForkWorkflowStep[],
	runStatus: string,
): ForkWorkflowPhaseState => {
	const currentIndex = phases.indexOf(currentPhase);
	const passed = currentIndex > index;
	if (currentIndex < index) {
		return "pending";
	}
	const phaseSteps = steps.filter((step) => step.phase === phase);
	const state = forkWorkflowPhaseState(phaseSteps);
	// A phase the script moved past is done, and on a completed run every
	// reached phase is done: the script finished it or moved past it. On a
	// live run a passed phase keeps moving until its steps settle, because
	// a spawn() step can outlive the phase that started it.
	if (passed || runStatus === "completed") {
		if (
			runStatus === "running" &&
			phaseSteps.some((step) => step.status !== "reported")
		) {
			return state === "failed" ? "failed" : "running";
		}
		return "done";
	}
	if (state === "pending" && phaseSteps.length === 0) {
		// The phase spawned no steps. The phase the run ended in failed
		// with the run. On a live run it is still starting.
		return runEnded(runStatus) ? "failed" : "running";
	}
	return state;
};

/**
 * Groups a run's steps by phase, in first-appearance order, which matches
 * the order the script reached the phases. Steps started before the first
 * phase() call land in the leading "" group, which is part of the list only
 * when such steps exist.
 */
export const forkWorkflowStepPhases = (
	steps: readonly ForkWorkflowStep[],
): readonly { phase: string; steps: readonly ForkWorkflowStep[] }[] => {
	const groups: { phase: string; steps: ForkWorkflowStep[] }[] = [];
	for (const step of steps) {
		const group = groups.find((entry) => entry.phase === step.phase);
		if (group) {
			group.steps.push(step);
		} else {
			groups.push({ phase: step.phase, steps: [step] });
		}
	}
	return groups;
};

/** Summarizes step progress, for example "2 of 3 steps reported". */
export const forkWorkflowStepCountProgress = (
	reported: number,
	total: number,
): string => {
	if (total === 0) {
		return "No steps yet";
	}
	const noun = total === 1 ? "step" : "steps";
	return `${reported} of ${total} ${noun} reported`;
};

export const forkWorkflowStepProgress = (
	steps: readonly ForkWorkflowStep[],
): string =>
	forkWorkflowStepCountProgress(
		steps.filter((step) => step.status === "reported").length,
		steps.length,
	);

/** Formats how long a run took, or has run so far while it runs. */
export const forkWorkflowRunDuration = (
	run: Pick<
		WorkflowRun,
		"status" | "created_at" | "updated_at" | "finished_at"
	>,
	now: number,
): string => {
	let end = now;
	if (run.finished_at) {
		end = Date.parse(run.finished_at);
	} else if (run.status !== "running") {
		// An interrupted run has no end until a resume ends it, but it stopped
		// making progress at its last update.
		end = Date.parse(run.updated_at);
	}
	return humanDurationShort(Math.max(0, end - Date.parse(run.created_at)));
};

/** Formats a JSON value for display; strings show as they are. */
export const formatForkWorkflowValue = (value: unknown): string =>
	typeof value === "string" ? value : JSON.stringify(value, null, 2);
