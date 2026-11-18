// This file is fork code: the fork-workflows experiment's run API calls live
// here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { WorkflowRun, WorkflowRunDetail } from "./typesGenerated";

/** The page size of the run list, matching the server default. */
export const workflowRunsPageSize = 50;

/** The largest page the run list endpoint serves. */
export const workflowRunsMaxLimit = 100;

/**
 * A number that lost precision to the JavaScript number format, holding
 * the exact text it was parsed from. Only integers outside the safe range
 * get this wrapper, and JSON.stringify of a number never emits a decimal
 * point, so the source text is the digits of the number itself.
 */
export type UnsafeInteger = {
	forkUnsafeInteger: string;
};

/** Whether a value came from parseForkWorkflowJson as an unsafe integer. */
export const isUnsafeInteger = (value: unknown): value is UnsafeInteger =>
	typeof value === "object" &&
	value !== null &&
	"forkUnsafeInteger" in value &&
	typeof (value as UnsafeInteger).forkUnsafeInteger === "string" &&
	Object.keys(value).length === 1;

/**
 * Parses JSON text keeping every integer exact. The JSON.parse reviver
 * receives a value the parser already rounded, so the exact digits come
 * from context.source, which V8-based runtimes expose for number and
 * bigint literals. Where it is missing the rounded number stays, matching
 * the behavior of the values this fork does not parse itself.
 */
/** The third argument V8 hands the JSON.parse reviver for literals. */
type ReviverContext = { source?: string };

export const parseForkWorkflowJson = (text: string): unknown =>
	JSON.parse(
		text,
		(_key: string, value: unknown, context?: ReviverContext): unknown => {
			if (
				typeof value === "number" &&
				Number.isInteger(value) &&
				!Number.isSafeInteger(value)
			) {
				const source = context?.source;
				if (typeof source === "string" && Number(source) === value) {
					return { forkUnsafeInteger: source } satisfies UnsafeInteger;
				}
			}
			return value;
		},
	);

/** Fetches the chat's workflow runs, newest first. */
export const getChatWorkflowRuns = async (
	chatId: string,
	after: string,
	signal?: AbortSignal,
	limit = workflowRunsPageSize,
): Promise<WorkflowRun[]> => {
	const response = await API.getAxiosInstance().get<WorkflowRun[]>(
		`/api/v2/chats/fork/${chatId}/workflow-runs`,
		{ signal, params: { limit, after } },
	);
	return response.data;
};

/**
 * Fetches one workflow run with its script, logs and steps. The detail's
 * args, result and step values can hold integers outside the safe range,
 * so the response parses with a lossless reviver instead of the axios
 * default, which would round them before display.
 */
export const getChatWorkflowRun = async (
	chatId: string,
	runId: string,
	signal?: AbortSignal,
): Promise<WorkflowRunDetail> => {
	const response = await API.getAxiosInstance().get<WorkflowRunDetail>(
		`/api/v2/chats/fork/${chatId}/workflow-runs/${runId}`,
		{
			signal,
			transformResponse: [(data: string) => parseForkWorkflowJson(data)],
		},
	);
	return response.data;
};

/** Stops a running workflow run and returns the run as it ended. */
export const stopChatWorkflowRun = async (
	chatId: string,
	runId: string,
): Promise<WorkflowRunDetail> => {
	const response = await API.getAxiosInstance().post<WorkflowRunDetail>(
		`/api/v2/chats/fork/${chatId}/workflow-runs/${runId}/stop`,
	);
	return response.data;
};
