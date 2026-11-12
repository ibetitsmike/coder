// This file is fork code: the fork-workflows experiment's run API calls live
// here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";

// TODO(workflows-ui): replace these hand-written types with the generated
// codersdk types in typesGenerated.ts.

export type ChatWorkflowRunStatus =
	| "running"
	| "interrupted"
	| "completed"
	| "failed"
	| "stopped";

type ChatWorkflowRunBase = {
	readonly id: string;
	readonly chat_id: string;
	readonly status: ChatWorkflowRunStatus;
	readonly title: string;
	readonly error: string;
	/** What main(args) returned; null until the run ends. */
	readonly result: unknown;
	readonly created_at: string;
	readonly updated_at: string;
	readonly finished_at?: string;
};

type ChatWorkflowRunStepCounts = {
	readonly total: number;
	readonly running: number;
	readonly done: number;
	readonly failed: number;
};

/** A run as the run list returns it. */
export type ChatWorkflowRun = ChatWorkflowRunBase & {
	/** The script's first characters on one line, without an ellipsis. */
	readonly script_preview: string;
	readonly step_counts: ChatWorkflowRunStepCounts;
};

export type ChatWorkflowRunStep = {
	/** The step's child chat ID. */
	readonly id: string;
	readonly run_id: string;
	readonly title: string;
	readonly status: string;
	readonly reused: boolean;
	readonly error: string;
	readonly ordinal: number;
	readonly started_at: string;
	readonly finished_at?: string;
	readonly value: unknown;
};

export type ChatWorkflowRunDetail = ChatWorkflowRunBase & {
	readonly script: string;
	/** Empty while the run is running: logs are written when it ends. */
	readonly logs: readonly string[];
	readonly steps: readonly ChatWorkflowRunStep[];
};

/** Fetches the chat's workflow runs, newest first. */
export const getChatWorkflowRuns = async (
	chatId: string,
	signal?: AbortSignal,
): Promise<ChatWorkflowRun[]> => {
	const response = await API.getAxiosInstance().get<ChatWorkflowRun[]>(
		`/api/v2/chats/fork/${chatId}/workflow-runs`,
		{ signal },
	);
	return response.data;
};

/** Fetches one workflow run with its script, logs and steps. */
export const getChatWorkflowRun = async (
	chatId: string,
	runId: string,
	signal?: AbortSignal,
): Promise<ChatWorkflowRunDetail> => {
	const response = await API.getAxiosInstance().get<ChatWorkflowRunDetail>(
		`/api/v2/chats/fork/${chatId}/workflow-runs/${runId}`,
		{ signal },
	);
	return response.data;
};
