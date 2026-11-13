import { mutationOptions, type QueryClient, queryOptions } from "react-query";
import {
	getChatWorkflowRun,
	getChatWorkflowRuns,
	stopChatWorkflowRun,
} from "#/api/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";

/** Also prefixes each run's key, so invalidating it refetches the details. */
export const chatWorkflowRunsKey = (chatId: string) =>
	["chats", "fork-workflow-runs", chatId] as const;

export const chatWorkflowRuns = (chatId: string) =>
	queryOptions({
		queryKey: chatWorkflowRunsKey(chatId),
		queryFn: ({ signal }) => getChatWorkflowRuns(chatId, signal),
		// Runs have no watch socket: poll faster while one is running, and
		// slowly otherwise so a run the agent starts shows up.
		refetchInterval: ({ state }) =>
			state.data?.some((run) => run.status === "running") ? 5_000 : 30_000,
	});

export const chatWorkflowRunKey = (chatId: string, runId: string) =>
	[...chatWorkflowRunsKey(chatId), runId] as const;

/** Polls a run's detail while the run is running. */
export const workflowRunRefetchInterval = (status: string | undefined) =>
	status === "running" ? 3_000 : false;

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
