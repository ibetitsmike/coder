// This file is fork code: it parses the Starlark workflow tool results of
// the fork-workflows experiment and maps their statuses to display values.

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
	createdAt: string,
	finishedAt: string | undefined,
	now: number,
): string => {
	const end = finishedAt ? Date.parse(finishedAt) : now;
	return humanDurationShort(Math.max(0, end - Date.parse(createdAt)));
};

/** Formats a JSON value for display; strings show as they are. */
export const formatForkWorkflowValue = (value: unknown): string =>
	typeof value === "string" ? value : JSON.stringify(value, null, 2);
