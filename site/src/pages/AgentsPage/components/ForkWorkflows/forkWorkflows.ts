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
};

/** A run as run_workflow and await_workflow report it. */
export type ForkWorkflowRunSnapshot = {
	runId: string;
	status: string;
	/** What main(args) returned; undefined until the run completes. */
	result: unknown;
	error: string;
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
	return {
		runId,
		status,
		result: record.result,
		error: asString(record.error),
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
		}));

/** Lets the tool cards render a run detail like a tool result. */
export const forkWorkflowRunDetailSnapshot = (
	run: WorkflowRunDetail,
): ForkWorkflowRunSnapshot => ({
	runId: run.id,
	status: run.status,
	result: run.result,
	error: run.error,
	steps: forkWorkflowRunDetailSteps(run),
	logs: run.logs,
});

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
