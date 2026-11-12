import { queryOptions } from "react-query";
import { getChatWorkflowRun, getChatWorkflowRuns } from "#/api/forkWorkflows";

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

/** Polls the run every 3 s until it leaves the running status. */
export const chatWorkflowRun = (chatId: string, runId: string) =>
	queryOptions({
		queryKey: [...chatWorkflowRunsKey(chatId), runId],
		queryFn: ({ signal }) => getChatWorkflowRun(chatId, runId, signal),
		refetchInterval: ({ state }) =>
			state.data?.status === "running" ? 3_000 : false,
	});
