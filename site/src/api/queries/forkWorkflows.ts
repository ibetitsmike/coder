import { queryOptions } from "react-query";
import { getChatWorkflowRun, getChatWorkflowRuns } from "#/api/forkWorkflows";

/** Query key for a chat's workflow runs. */
export const chatWorkflowRunsKey = (chatId: string) =>
	["chats", "fork-workflow-runs", chatId] as const;

/** Query key for one workflow run. */
export const chatWorkflowRunKey = (chatId: string, runId: string) =>
	[...chatWorkflowRunsKey(chatId), runId] as const;

/** Query options for a chat's workflow runs, newest first. */
export const chatWorkflowRuns = (chatId: string) =>
	queryOptions({
		queryKey: chatWorkflowRunsKey(chatId),
		queryFn: ({ signal }) => getChatWorkflowRuns(chatId, signal),
		// Runs have no watch socket: poll faster while one is running, and
		// slowly otherwise so a run the agent starts shows up.
		refetchInterval: ({ state }) =>
			state.data?.some((run) => run.status === "running") ? 5_000 : 30_000,
	});

/** Query options for one workflow run, polled while it runs. */
export const chatWorkflowRun = (chatId: string, runId: string) =>
	queryOptions({
		queryKey: chatWorkflowRunKey(chatId, runId),
		queryFn: ({ signal }) => getChatWorkflowRun(chatId, runId, signal),
		refetchInterval: ({ state }) =>
			state.data?.status === "running" ? 3_000 : false,
	});
