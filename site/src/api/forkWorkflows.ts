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

/** Whether a value came from the lossless parse as an unsafe integer. */
export const isUnsafeInteger = (value: unknown): value is UnsafeInteger =>
	typeof value === "object" &&
	value !== null &&
	"forkUnsafeInteger" in value &&
	typeof value.forkUnsafeInteger === "string" &&
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

/**
 * Wraps text as raw JSON, so JSON.stringify prints it without quotes.
 * JSON.rawJSON ships with the same V8 feature as the reviver's
 * context.source; the type does not declare it yet, so it is reached
 * through a feature check and the quoted string is the fallback. The
 * JSON.rawJSON constructor throws on text that is not valid JSON, so
 * the guard only feeds it digits.
 */
const rawJSON = (text: string): unknown => {
	if (!/^-?\d+$/.test(text)) {
		return text;
	}
	const f = (JSON as { rawJSON?: (raw: string) => unknown }).rawJSON;
	return typeof f === "function" ? f.call(JSON, text) : text;
};

/**
 * The exact text of an unsafe integer for display: bare digits where the
 * runtime can stringify them as raw JSON, a quoted string where it
 * cannot, so the digits still show. A forkUnsafeInteger text is always
 * the digits the reviver copied from context.source, which the reviver
 * guards to integer literals.
 */
export const unsafeIntegerText = (value: UnsafeInteger): unknown =>
	rawJSON(value.forkUnsafeInteger);

/**
 * The reviver axios hands to JSON.parse for the run detail body. It wraps
 * every integer literal outside the safe range, including one that
 * overflows the number format to +-Infinity, whose digits stay exact. A
 * decimal source, such as 9007199254740993.5, rounds like plain JSON and
 * the wrapper never holds a fractional text.
 */
export const forkWorkflowReviver = (
	_key: string,
	value: unknown,
	context?: ReviverContext,
): unknown => {
	if (typeof value !== "number") {
		return value;
	}
	const source = context?.source;
	if (typeof source !== "string" || !/^-?\d+$/.test(source)) {
		return value;
	}
	if (Number.isSafeInteger(value)) {
		return value;
	}
	return { forkUnsafeInteger: source } satisfies UnsafeInteger;
};

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
 * args, result and step values can hold integers outside the safe
 * range, so the response parses through a lossless reviver, handed to
 * axios as parseReviver. That keeps axios's error-path parsing (a
 * proxy error page or an empty body must still surface as an
 * AxiosError) and only changes how success bodies decode.
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
			// Axios calls the reviver itself for every value of the body;
			// handing it a wrapper that parses the whole document breaks
			// the first value it visits.
			parseReviver: forkWorkflowReviver,
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
		undefined,
		// The stop response carries the run's args and step values, so it
		// needs the same lossless parsing as the detail before it lands in
		// the detail cache.
		{ parseReviver: forkWorkflowReviver },
	);
	return response.data;
};
