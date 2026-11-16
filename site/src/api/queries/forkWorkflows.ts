import {
	infiniteQueryOptions,
	mutationOptions,
	type QueryClient,
	queryOptions,
} from "react-query";
import {
	getChatWorkflowRun,
	getChatWorkflowRuns,
	stopChatWorkflowRun,
} from "#/api/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";

/** Also prefixes each run's key, so invalidating it refetches the details. */
export const chatWorkflowRunsKey = (chatId: string) =>
	["chats", "fork-workflow-runs", chatId] as const;

/** The page size of the run list. It matches the server default. */
const workflowRunsPageSize = 50;

/**
 * Lists the chat's workflow runs, newest first, as pages that "Load older
 * runs" extends with the keyset cursor of the last loaded run.
 */
export const chatWorkflowRuns = (chatId: string) =>
	infiniteQueryOptions({
		queryKey: chatWorkflowRunsKey(chatId),
		queryFn: ({ signal, pageParam }) =>
			getChatWorkflowRuns(chatId, pageParam.after, signal),
		initialPageParam: { after: "" },
		getNextPageParam: (lastPage) => {
			const last = lastPage[lastPage.length - 1];
			// A short page has no older runs behind it.
			if (!last || lastPage.length < workflowRunsPageSize) {
				return undefined;
			}
			return { after: `${last.created_at}/${last.id}` };
		},
		// Runs have no watch socket: poll faster while one is running, and
		// slowly otherwise so a run the agent starts shows up.
		refetchInterval: ({ state }) =>
			state.data?.pages[0]?.some((run) => run.status === "running")
				? 5_000
				: 30_000,
	});

/** Keys one run's detail under its chat's runs. */
export const chatWorkflowRunKey = (chatId: string, runId: string) =>
	[...chatWorkflowRunsKey(chatId), runId] as const;

/** The detail query and the live tool card share this polling policy. */
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
