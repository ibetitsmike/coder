import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getChatWorkflowRun,
	getChatWorkflowRuns,
	stopChatWorkflowRun,
} from "#/api/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { forkPanelResyncInterval } from "./forkPanelResync";

export const chatWorkflowRunsRootKey = ["chats", "fork-workflow-runs"] as const;

/** Also prefixes each run's key, so invalidating it refetches the details. */
export const chatWorkflowRunsKey = (chatId: string) =>
	[...chatWorkflowRunsRootKey, chatId] as const;

export const chatWorkflowRuns = (chatId: string) =>
	queryOptions({
		queryKey: chatWorkflowRunsKey(chatId),
		queryFn: ({ signal }) => getChatWorkflowRuns(chatId, signal),
		refetchInterval: forkPanelResyncInterval,
	});

/** Keys one run's detail under its chat's runs. */
export const chatWorkflowRunKey = (chatId: string, runId: string) =>
	[...chatWorkflowRunsKey(chatId), runId] as const;

/** The detail query and the live tool card share this polling policy. */
export const workflowRunRefetchInterval = (status: string | undefined) =>
	status === "running" ? forkPanelResyncInterval : false;

export const chatWorkflowRun = (chatId: string, runId: string) =>
	queryOptions({
		queryKey: chatWorkflowRunKey(chatId, runId),
		queryFn: ({ signal }) => getChatWorkflowRun(chatId, runId, signal),
		refetchInterval: ({ state }) =>
			workflowRunRefetchInterval(state.data?.status),
	});

/** Stops a run, then refreshes the chat's runs and their details. */
export const stopWorkflowRun = (
	queryClient: QueryClient,
	chatId: string,
	runId: string,
) =>
	mutationOptions({
		mutationFn: () => stopChatWorkflowRun(chatId, runId),
		onSuccess: (run: WorkflowRunDetail) => {
			queryClient.setQueryData(chatWorkflowRunKey(chatId, runId), run);
		},
		// A 409 means the run is no longer stoppable, so refetch either way.
		onSettled: () =>
			queryClient.invalidateQueries({ queryKey: chatWorkflowRunsKey(chatId) }),
	});
