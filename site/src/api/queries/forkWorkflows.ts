import {
	type InfiniteData,
	infiniteQueryOptions,
	mutationOptions,
	type QueryClient,
	queryOptions,
} from "react-query";
import {
	getChatWorkflowRun,
	getChatWorkflowRuns,
	stopChatWorkflowRun,
	workflowRunsPageSize,
} from "#/api/forkWorkflows";
import type { WorkflowRun, WorkflowRunDetail } from "#/api/typesGenerated";

/** Also prefixes each run's key, so invalidating it refetches the details. */
export const chatWorkflowRunsKey = (chatId: string) =>
	["chats", "fork-workflow-runs", chatId] as const;

type WorkflowRunPageParam = { after: string };

/** Whether one of the run list's loaded pages still has a running run. */
const hasRunningRun = (data: InfiniteData<WorkflowRun[]> | undefined) =>
	data?.pages.some((page) => page.some((run) => run.status === "running")) ??
	false;

/**
 * Lists the chat's workflow runs, newest first, as pages that "Load older
 * runs" extends with the keyset cursor of the last loaded run.
 */
export const chatWorkflowRuns = (chatId: string) =>
	infiniteQueryOptions({
		queryKey: chatWorkflowRunsKey(chatId),
		queryFn: ({ client, queryKey, signal, pageParam }) => {
			// A refetch walks every loaded page, one request per page. Older
			// pages can only change behind their cursor, so when nothing on
			// them runs, reuse the cached page instead of fetching it: steady
			// state costs one request per tick. When a page runs, it is
			// fetched, and a new run shifts the first page's cursor, so the
			// page behind it is fetched with its new cursor rather than
			// reused.
			if (pageParam.after !== "") {
				const cached =
					client.getQueryData<
						InfiniteData<WorkflowRun[], WorkflowRunPageParam>
					>(queryKey);
				const index = cached?.pageParams.findIndex(
					(param) => param.after === pageParam.after,
				);
				const page = cached?.pages[index ?? -1];
				if (page && !page.some((run) => run.status === "running")) {
					return page;
				}
			}
			return getChatWorkflowRuns(chatId, pageParam.after, signal);
		},
		initialPageParam: { after: "" } as WorkflowRunPageParam,
		getNextPageParam: (lastPage): WorkflowRunPageParam | undefined => {
			const last = lastPage[lastPage.length - 1];
			// A short page has no older runs behind it.
			if (!last || lastPage.length < workflowRunsPageSize) {
				return undefined;
			}
			return { after: `${last.created_at}/${last.id}` };
		},
		// Runs have no watch socket: poll faster while any loaded page has a
		// run going, and slowly otherwise so a run the agent starts shows up.
		refetchInterval: ({ state }) =>
			hasRunningRun(state.data) ? 5_000 : 30_000,
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
