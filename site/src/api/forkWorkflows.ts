// This file is fork code: the fork-workflows experiment's run API calls live
// here so api.ts, an upstream file, stays unchanged.

import { API } from "./api";
import type { WorkflowRun, WorkflowRunDetail } from "./typesGenerated";

/** Fetches the chat's workflow runs, newest first. */
export const getChatWorkflowRuns = async (
	chatId: string,
	signal?: AbortSignal,
): Promise<WorkflowRun[]> => {
	const response = await API.getAxiosInstance().get<WorkflowRun[]>(
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
): Promise<WorkflowRunDetail> => {
	const response = await API.getAxiosInstance().get<WorkflowRunDetail>(
		`/api/v2/chats/fork/${chatId}/workflow-runs/${runId}`,
		{ signal },
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
