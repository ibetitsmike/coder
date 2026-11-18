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
	workflowRunsMaxLimit,
	workflowRunsPageSize,
} from "#/api/forkWorkflows";
import type { WorkflowRun, WorkflowRunDetail } from "#/api/typesGenerated";
import { forkPanelResyncInterval } from "./forkPanelResync";

export const chatWorkflowRunsRootKey = ["chats", "fork-workflow-runs"] as const;

/** Also prefixes each run's key, so invalidating it refetches the details. */
export const chatWorkflowRunsKey = (chatId: string) =>
	[...chatWorkflowRunsRootKey, chatId] as const;

export type WorkflowRunPageParam = { after: string };

/**
 * An RFC 3339 timestamp as whole seconds and nanosecond digits. The server
 * trims trailing zeros and may use an offset, so the text does not sort.
 */
const keysetTime = (stamp: string) => ({
	seconds: Date.parse(stamp.replace(/\.\d+/, "")),
	fraction: (/\.(\d+)/.exec(stamp)?.[1] ?? "").padEnd(9, "0"),
});

/** The keyset order of the run list: newest first, created_at then id. */
const isNewerRun = (run: WorkflowRun, than: WorkflowRun) => {
	const a = keysetTime(run.created_at);
	const b = keysetTime(than.created_at);
	if (a.seconds !== b.seconds) {
		return a.seconds > b.seconds;
	}
	if (a.fraction !== b.fraction) {
		return a.fraction > b.fraction;
	}
	return run.id > than.id;
};

/**
 * Lists the chat's workflow runs, newest first, as pages that "Load older
 * runs" extends with the keyset cursor of the last loaded run.
 */
export const chatWorkflowRuns = (chatId: string) =>
	infiniteQueryOptions({
		queryKey: chatWorkflowRunsKey(chatId),
		queryFn: async ({ client, queryKey, signal, pageParam }) => {
			// A refetch revisits every loaded page: a resume flips an
			// interrupted run to running in place, without moving its cursor,
			// so only a fetch can notice it, and a new run shifts the first
			// page's cursor, so the page behind it is fetched with its new
			// cursor. Each fetch is also the reconcile with the server, so a
			// run the server's retention has purged drops out of the cache.
			const cached =
				client.getQueryData<InfiniteData<WorkflowRun[], WorkflowRunPageParam>>(
					queryKey,
				);
			const pages = cached?.pages ?? [];
			const index =
				cached?.pageParams.findIndex(
					(param) => param.after === pageParam.after,
				) ?? -1;
			const cachedPage = pages[index];
			// A refetch asks a page for as many rows as it holds, so a page a
			// top-up grew keeps its tail and the page behind it its cursor.
			const limit = Math.min(
				workflowRunsMaxLimit,
				Math.max(workflowRunsPageSize, cachedPage?.length ?? 0),
			);
			const runs = await getChatWorkflowRuns(
				chatId,
				pageParam.after,
				signal,
				limit,
			);
			// A tick that lands between two keyset pages walks the shifted
			// cursors, so d new runs leave the d oldest cached rows unfetched
			// and they fall out of the cache. The last page of the walk tops
			// up with those rows, so the flat list stays whole; a page whose
			// tail is not past the second to last page's tail has pages
			// behind it that fetch them. With 50 or more rows to make up, or
			// a page that would pass the server's 100-row limit, the oldest
			// rows still fall out, and Load older brings them back.
			const tail = runs[runs.length - 1];
			const boundary = pages[pages.length - 2]?.at(-1);
			if (
				runs.length === limit &&
				tail &&
				(!boundary || isNewerRun(boundary, tail))
			) {
				const remaining = pages
					.flat()
					.filter((run) => isNewerRun(tail, run)).length;
				if (
					remaining > 0 &&
					remaining < workflowRunsPageSize &&
					runs.length + remaining <= workflowRunsMaxLimit
				) {
					const topUp = await getChatWorkflowRuns(
						chatId,
						`${tail.created_at}/${tail.id}`,
						signal,
						remaining,
					);
					const seen = new Set(runs.map((run) => run.id));
					return [...runs, ...topUp.filter((run) => !seen.has(run.id))];
				}
			}
			return runs;
		},
		initialPageParam: { after: "" } satisfies WorkflowRunPageParam,
		getNextPageParam: (lastPage): WorkflowRunPageParam | undefined => {
			const last = lastPage[lastPage.length - 1];
			// A short page has no older runs behind it.
			if (!last || lastPage.length < workflowRunsPageSize) {
				return undefined;
			}
			return { after: `${last.created_at}/${last.id}` };
		},
		// The watch stream invalidates a run's panels on each change; this
		// resync tick catches what it misses. Its refetch walks every loaded
		// page, so it is also the reconcile with the server: a run the
		// retention has purged drops out of the cache within one interval.
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
