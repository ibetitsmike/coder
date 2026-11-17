// This file is fork code: the polling behavior of the workflow run list
// query (fork-workflows experiment). A refetch walks every loaded page, so
// these tests pin that steady state costs one request per tick and that a
// running, resumed or shifted older page is still fetched, not reused.

import {
	type InfiniteData,
	InfiniteQueryObserver,
	QueryClient,
} from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import type { WorkflowRun } from "#/api/typesGenerated";
import { MockChatWorkflowRun } from "#/testHelpers/forkWorkflows";
import {
	chatWorkflowRuns,
	chatWorkflowRunsKey,
	runListHasRunningRun,
	type WorkflowRunPageParam,
} from "./forkWorkflows";

/**
 * The cursor of a page: the keyset address of its last (oldest) run.
 * makeRun and fullPage share this stamp so the cursor of a page built
 * from indexes matches the one the query would compute from the runs.
 */
const stampOf = (index: number) => {
	const stamp = (index + 24_000) * 3;
	return `2026-11-12T${String(Math.floor(stamp / 3600) % 24).padStart(2, "0")}:${String(Math.floor((stamp % 3600) / 60)).padStart(2, "0")}:${String(stamp % 60).padStart(2, "0")}Z`;
};

/** Timestamps grow with the index, so a higher index is a newer run. */
const makeRun = (index: number, status: string): WorkflowRun => ({
	...MockChatWorkflowRun,
	id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
	status,
	created_at: stampOf(index),
});

/**
 * One page of the server's answer: the newest 50 runs first, then older
 * pages behind the keyset cursor. Every run is unique, and its timestamp
 * is unique too, so a cursor always names exactly one run.
 */
const fullPage = (firstIndex: number, statuses: readonly string[]) =>
	statuses.map((status, offset) => makeRun(firstIndex - offset, status));

const cursorOf = (page: readonly WorkflowRun[]) =>
	`${page[page.length - 1].created_at}/${page[page.length - 1].id}`;

const observeRuns = () => {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	});
	const getRuns = vi
		.spyOn(forkWorkflowsApi, "getChatWorkflowRuns")
		.mockResolvedValue([]);
	const observer = new InfiniteQueryObserver(
		queryClient,
		chatWorkflowRuns("chat-1"),
	);
	// Subscribe last, after the test has queued its page mocks, so the
	// subscription's first fetch reads them in order.
	let unsubscribe = () => {};
	const start = () => {
		unsubscribe = observer.subscribe(() => {});
	};
	const fetchNextPage = async () => {
		const result = await observer.fetchNextPage();
		return result;
	};
	return {
		queryClient,
		getRuns,
		pages: () => observer.getCurrentResult().data?.pages ?? [],
		hasNextPage: () => observer.getCurrentResult().hasNextPage,
		refetch: () => observer.refetch(),
		fetchNextPage,
		start,
		unsubscribe: () => unsubscribe(),
	};
};

/**
 * A server holding runs total - 1 down to 0 that answers the keyset
 * query: the newest rows, or the rows behind a cursor, up to the limit.
 */
const serveRuns =
	(total: number) =>
	(_chatId: string, after: string, _signal?: AbortSignal, limit = 50) => {
		const all = fullPage(total - 1, new Array(total).fill("completed"));
		const start =
			after === ""
				? 0
				: all.findIndex((run) => `${run.created_at}/${run.id}` === after) + 1;
		return Promise.resolve(all.slice(start, start + limit));
	};

/** Waits for the observer to hold the given number of pages. */
const waitForPages = async (pages: () => readonly unknown[], count: number) => {
	await vi.waitFor(() => expect(pages()).toHaveLength(count));
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("chatWorkflowRuns polling", () => {
	it("makes one request per tick with two finished pages", async () => {
		const { getRuns, pages, fetchNextPage, refetch, start, unsubscribe } =
			observeRuns();
		try {
			// The list is newest first: run 49 is the oldest run of the
			// first page, and the cursor page holds the 50 runs behind it.
			const first = fullPage(49, new Array(50).fill("completed"));
			const second = fullPage(-1, new Array(50).fill("completed"));
			const cursor = cursorOf(first);
			getRuns
				.mockResolvedValueOnce(first)
				.mockResolvedValueOnce(second)
				.mockResolvedValue(first);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);
			expect(getRuns.mock.calls[1][1]).toBe(cursor);

			// One tick fetches the first page; the finished older page is
			// reused from the cache, so its cursor is absent from the calls.
			// The refetch resolves only after it has walked every page.
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => call[1])).toEqual([""]);
		} finally {
			unsubscribe();
		}
	});

	it("refetches a page that holds a running run", async () => {
		const { getRuns, pages, fetchNextPage, refetch, start, unsubscribe } =
			observeRuns();
		try {
			// The older page holds a running run, addressed by the first
			// page's cursor.
			const first = fullPage(49, new Array(50).fill("completed"));
			const runningPage = fullPage(-1, ["completed", "running", "completed"]);
			const cursor = cursorOf(first);
			getRuns
				.mockResolvedValueOnce(first)
				.mockResolvedValueOnce(runningPage)
				.mockResolvedValue(first);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);

			// The tick fetches the running page again; the finished first
			// page is fetched too, since the first page is always fetched
			// fresh.
			getRuns.mockResolvedValueOnce(first).mockResolvedValueOnce(runningPage);
			getRuns.mockClear();
			refetch();
			await vi.waitFor(() => expect(getRuns).toHaveBeenCalledTimes(2));
			expect(getRuns.mock.calls.map((call) => call[1])).toEqual(["", cursor]);
		} finally {
			unsubscribe();
		}
	});

	it("refetches a page whose interrupted run a resume set to running", async () => {
		const { getRuns, pages, fetchNextPage, refetch, start, unsubscribe } =
			observeRuns();
		try {
			// The older page holds an interrupted run. A resume flips the
			// row to running in place, so the cursor never moves and only a
			// fetch can notice the change.
			const first = fullPage(49, new Array(50).fill("completed"));
			const cursor = cursorOf(first);
			getRuns
				.mockResolvedValueOnce(first)
				.mockResolvedValueOnce(
					fullPage(-1, ["completed", "interrupted", "completed"]),
				)
				.mockResolvedValue(first);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);

			getRuns
				.mockResolvedValueOnce(first)
				// The resume already happened when this tick asks the page.
				.mockResolvedValueOnce(
					fullPage(-1, ["completed", "running", "completed"]),
				);
			getRuns.mockClear();
			refetch();
			await vi.waitFor(() =>
				expect(
					pages()[1].some((run: WorkflowRun) => run.status === "running"),
				).toBe(true),
			);
			await vi.waitFor(() => expect(getRuns).toHaveBeenCalledTimes(2));
			expect(getRuns.mock.calls.map((call) => call[1])).toEqual(["", cursor]);
		} finally {
			unsubscribe();
		}
	});

	it("refetches the page behind a shifted cursor when a new run arrives", async () => {
		const { getRuns, pages, fetchNextPage, refetch, start, unsubscribe } =
			observeRuns();
		try {
			// The first page ends at run 49 and the cursor page starts at
			// run -1, so run 50 does not exist yet.
			const first = fullPage(49, new Array(50).fill("completed"));
			const cursor = cursorOf(first);
			getRuns
				.mockResolvedValueOnce(first)
				.mockResolvedValueOnce(fullPage(-1, new Array(50).fill("completed")))
				.mockResolvedValue(first);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);
			expect(getRuns.mock.calls[1][1]).toBe(cursor);

			// The tick's first page now leads with a newer run 50, so its
			// last (oldest) run moves forward too: the server keeps each
			// page at 50 runs, so run 0 falls onto the page behind and the
			// first page ends at run 1 instead of run 0. The page behind
			// must be fetched with the new cursor, not reused from the
			// cache, and it holds the run that moved off the first page.
			const shiftedFirst = [makeRun(50, "completed"), ...first.slice(0, 49)];
			const newCursor = cursorOf(shiftedFirst);
			expect(newCursor === cursor).toBe(false);
			getRuns.mockImplementation((_chatId, after) =>
				Promise.resolve(
					after === ""
						? shiftedFirst
						: after === newCursor
							? fullPage(0, new Array(50).fill("completed"))
							: [],
				),
			);
			getRuns.mockClear();
			refetch();
			await vi.waitFor(() => expect(getRuns).toHaveBeenCalledTimes(3));
			// The shifted page drops the old tail run -50 from the cache,
			// so the walk also tops the page up with that one row.
			expect(getRuns.mock.calls.map((call) => call[1])).toEqual([
				"",
				newCursor,
				cursorOf(fullPage(0, new Array(50).fill("completed"))),
			]);
		} finally {
			unsubscribe();
		}
	});
});

describe("chatWorkflowRuns refetch top-up", () => {
	it("keeps the old tail when a refetch lands between pages", async () => {
		const { getRuns, pages, fetchNextPage, refetch, start, unsubscribe } =
			observeRuns();
		try {
			// Two loaded pages: runs 49 down to -50. Run 50 then arrives,
			// so a fresh first page ends at run 1 and the tick's walk of
			// the shifted cursors never lands on run -50, the old tail.
			const first = fullPage(49, new Array(50).fill("completed"));
			const second = fullPage(-1, new Array(50).fill("completed"));
			getRuns.mockResolvedValueOnce(first).mockResolvedValueOnce(second);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);

			const shiftedFirst = [makeRun(50, "completed"), ...first.slice(0, 49)];
			const newCursor = cursorOf(shiftedFirst);
			const shiftedSecond = fullPage(0, new Array(50).fill("completed"));
			getRuns.mockImplementation((_chatId, after, _signal, limit) =>
				Promise.resolve(
					limit === 1
						? [makeRun(-50, "completed")]
						: after === ""
							? shiftedFirst
							: after === newCursor
								? shiftedSecond
								: [],
				),
			);
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls).toHaveLength(3);
			// The top-up fetches exactly the one row the walk skipped.
			expect(getRuns.mock.calls[2]?.[1]).toBe(cursorOf(shiftedSecond));
			expect(getRuns.mock.calls[2]?.[3]).toBe(1);
			const flatIds = pages()
				.flat()
				.map((run: WorkflowRun) => run.id);
			expect(flatIds).toHaveLength(101);
			expect(flatIds).toEqual(
				Array.from({ length: 101 }, (_, i) => makeRun(50 - i, "completed").id),
			);
			expect(pages()[1]).toHaveLength(51);

			// With no new run the next tick fetches the first page only: its
			// tail is unchanged, so the topped-up page keeps its cursor and
			// comes from the cache.
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 50],
			]);
			expect(pages()[1]).toHaveLength(51);

			// Load older reads the top-up page's last row, so it must not
			// repeat the rows the top-up already covered.
			await fetchNextPage();
			const flatAfter = pages()
				.flat()
				.map((run: WorkflowRun) => run.id);
			expect(new Set(flatAfter).size).toBe(flatAfter.length);
			expect(flatAfter).toHaveLength(101);
		} finally {
			unsubscribe();
		}
	});

	it("does not top up when no run arrived", async () => {
		const { getRuns, pages, refetch, start, unsubscribe } = observeRuns();
		try {
			const first = fullPage(49, new Array(50).fill("completed"));
			getRuns.mockResolvedValue(first);
			start();
			await waitForPages(pages, 1);

			// The tick fetches the first page; its tail already covers
			// every cached row, so no top-up request follows.
			getRuns.mockClear();
			await refetch();
			expect(getRuns).toHaveBeenCalledTimes(1);
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 50],
			]);
		} finally {
			unsubscribe();
		}
	});

	it("drops the oldest rows when 50 or more runs arrive between polls", async () => {
		const {
			getRuns,
			pages,
			fetchNextPage,
			hasNextPage,
			refetch,
			start,
			unsubscribe,
		} = observeRuns();
		try {
			const first = fullPage(49, new Array(50).fill("completed"));
			getRuns.mockResolvedValueOnce(first);
			start();
			await waitForPages(pages, 1);

			// 60 new runs: the fresh first page ends at run 60, so all 50
			// cached rows are older than its tail. That is a page or more to
			// make up, so no top-up runs and the drop is accepted; Load
			// older brings the lost rows back.
			const shiftedFirst = fullPage(109, new Array(50).fill("completed"));
			const newCursor = cursorOf(shiftedFirst);
			getRuns.mockImplementation((_chatId, after) =>
				Promise.resolve(
					after === ""
						? shiftedFirst
						: after === newCursor
							? fullPage(59, new Array(50).fill("completed"))
							: [],
				),
			);
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 50],
			]);
			const flatIds = pages()
				.flat()
				.map((run: WorkflowRun) => run.id);
			expect(flatIds).toEqual(shiftedFirst.map((run) => run.id));
			expect(hasNextPage()).toBe(true);

			// Load older brings the dropped rows back with no duplicates.
			await fetchNextPage();
			const flatAfter = pages()
				.flat()
				.map((run: WorkflowRun) => run.id);
			expect(new Set(flatAfter).size).toBe(flatAfter.length);
			expect(flatAfter).toHaveLength(100);
		} finally {
			unsubscribe();
		}
	});

	it("tops up a single page and then polls it with one request", async () => {
		const { getRuns, pages, refetch, start, unsubscribe } = observeRuns();
		try {
			const first = fullPage(49, new Array(50).fill("completed"));
			getRuns.mockResolvedValueOnce(first);
			start();
			await waitForPages(pages, 1);

			// Three new runs push runs 2 to 0 off the fresh first page, so
			// the page tops up with them and holds 53 rows.
			const shiftedFirst = fullPage(52, new Array(50).fill("completed"));
			getRuns.mockImplementation((_chatId, after, _signal, limit) =>
				Promise.resolve(
					after === ""
						? fullPage(52, new Array(limit ?? 50).fill("completed"))
						: after === cursorOf(shiftedFirst)
							? fullPage(2, new Array(limit ?? 50).fill("completed"))
							: [],
				),
			);
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 50],
				[cursorOf(shiftedFirst), 3],
			]);
			expect(pages()[0]?.map((run: WorkflowRun) => run.id)).toEqual(
				fullPage(52, new Array(53).fill("completed")).map((run) => run.id),
			);

			// The next tick asks for all 53 rows, so its tail is the cached
			// tail and no top-up follows.
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 53],
			]);
			expect(pages()[0]).toHaveLength(53);
		} finally {
			unsubscribe();
		}
	});

	it("does not top up on fetchNextPage alone", async () => {
		const { getRuns, pages, fetchNextPage, start, unsubscribe } = observeRuns();
		try {
			const first = fullPage(49, new Array(50).fill("completed"));
			const second = fullPage(-1, new Array(50).fill("completed"));
			const third = fullPage(-101, new Array(50).fill("completed"));
			getRuns
				.mockResolvedValueOnce(first)
				.mockImplementation((_chatId, after) =>
					Promise.resolve(
						after === cursorOf(first)
							? second
							: after === cursorOf(second)
								? third
								: [],
					),
				);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);

			// The next page's tail is older than every cached row, so
			// nothing counts as remaining and no top-up request follows.
			getRuns.mockClear();
			await fetchNextPage();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				[cursorOf(second), 50],
			]);
			await waitForPages(pages, 3);
		} finally {
			unsubscribe();
		}
	});

	it("keeps a short last page whole without a top-up", async () => {
		const {
			getRuns,
			pages,
			fetchNextPage,
			hasNextPage,
			refetch,
			start,
			unsubscribe,
		} = observeRuns();
		try {
			// Runs 49 down to -10: a full first page and a short last page,
			// so there is nothing older to load.
			const first = fullPage(49, new Array(50).fill("completed"));
			const second = fullPage(-1, new Array(10).fill("completed"));
			getRuns.mockResolvedValueOnce(first).mockResolvedValueOnce(second);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);
			expect(hasNextPage()).toBe(false);

			// With no new run, the 10 rows older than the first page's tail
			// belong to the page behind it, so the first page does not top
			// up and the finished short page comes from the cache.
			getRuns.mockImplementation((_chatId, after) =>
				Promise.resolve(after === "" ? first : []),
			);
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 50],
			]);
			expect(pages().map((page) => page.length)).toEqual([50, 10]);

			// Five new runs: the fresh first page ends at run 5, and the page
			// behind its new cursor holds the 15 older rows, short, so the
			// walk covers every row and still nothing tops up.
			const shiftedFirst = fullPage(54, new Array(50).fill("completed"));
			const shiftedSecond = fullPage(4, new Array(15).fill("completed"));
			getRuns.mockImplementation((_chatId, after) =>
				Promise.resolve(
					after === ""
						? shiftedFirst
						: after === cursorOf(shiftedFirst)
							? shiftedSecond
							: [],
				),
			);
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 50],
				[cursorOf(shiftedFirst), 50],
			]);
			const flatIds = pages()
				.flat()
				.map((run: WorkflowRun) => run.id);
			expect(flatIds).toEqual(
				Array.from({ length: 65 }, (_, i) => makeRun(54 - i, "completed").id),
			);
			expect(hasNextPage()).toBe(false);
		} finally {
			unsubscribe();
		}
	});

	it("orders runs by time, not by timestamp text", async () => {
		const { getRuns, pages, refetch, start, unsubscribe } = observeRuns();
		try {
			// The server trims trailing zeros, so run 0 at a whole second
			// sorts after run 1 half a second later as text. Run 0 is still
			// the older run, and the top-up must count it.
			const first = fullPage(49, new Array(50).fill("completed"));
			const run0 = first[49];
			if (!run0) {
				throw new Error("the page must hold run 0");
			}
			// Run 1's id sorts below run 0's, so only the fraction of a
			// second orders the two.
			first[48] = {
				...makeRun(1, "completed"),
				id: "00000000-0000-3000-8000-000000000001",
				created_at: run0.created_at.replace("Z", ".5Z"),
			};
			getRuns.mockResolvedValueOnce(first);
			start();
			await waitForPages(pages, 1);

			const shiftedFirst = [makeRun(50, "completed"), ...first.slice(0, 49)];
			getRuns.mockImplementation((_chatId, after) =>
				Promise.resolve(
					after === ""
						? shiftedFirst
						: after === cursorOf(shiftedFirst)
							? [run0]
							: [],
				),
			);
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 50],
				[cursorOf(shiftedFirst), 1],
			]);
			expect(pages()[0]).toHaveLength(51);
		} finally {
			unsubscribe();
		}
	});

	it("keeps a single growing page at or below 100 rows", async () => {
		const { getRuns, pages, refetch, start, unsubscribe } = observeRuns();
		try {
			getRuns.mockImplementation(serveRuns(50));
			start();
			await waitForPages(pages, 1);

			// 49 new runs top the page up to 99 rows and one more to 100.
			// The next run would make 101, past the server's limit, so the
			// oldest row falls out instead.
			for (const [total, length] of [
				[99, 99],
				[100, 100],
				[101, 100],
			]) {
				getRuns.mockImplementation(serveRuns(total));
				await refetch();
				expect(pages()[0]).toHaveLength(length);
			}

			// With no new run the next tick asks for the page's 100 rows and
			// needs no top-up.
			getRuns.mockClear();
			await refetch();
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 100],
			]);
			expect(pages()[0]).toHaveLength(100);
		} finally {
			unsubscribe();
		}
	});

	it("asks for at most 100 rows for an oversized cached page", async () => {
		const { queryClient, getRuns, pages, start, unsubscribe } = observeRuns();
		try {
			// A page of 110 rows cached before the 100-row guard: the
			// refetch asks the server for its limit, not the page's length,
			// and does not top the page back up past it.
			queryClient.setQueryData<
				InfiniteData<WorkflowRun[], WorkflowRunPageParam>
			>(chatWorkflowRunsKey("chat-1"), {
				pages: [fullPage(109, new Array(110).fill("completed"))],
				pageParams: [{ after: "" }],
			});
			getRuns.mockImplementation(serveRuns(110));
			start();
			await vi.waitFor(() => expect(pages()[0]).toHaveLength(100));
			expect(getRuns.mock.calls.map((call) => [call[1], call[3]])).toEqual([
				["", 100],
			]);
		} finally {
			unsubscribe();
		}
	});
});

describe("runListHasRunningRun", () => {
	it("sees a running run on any loaded page, not just the first", () => {
		const first = fullPage(49, new Array(50).fill("completed"));
		const data = {
			pages: [first, fullPage(-1, ["completed", "running"])],
			pageParams: [
				{ after: "" },
				{ after: "c" },
			] satisfies WorkflowRunPageParam[],
		};
		expect(runListHasRunningRun(data)).toBe(true);
		expect(
			runListHasRunningRun({
				pages: [first, fullPage(-1, new Array(50).fill("completed"))],
				pageParams: [{ after: "" }, { after: "c" }],
			}),
		).toBe(false);
		expect(runListHasRunningRun(undefined)).toBe(false);
	});

	it("polls fast while any loaded page has a running run", () => {
		const { refetchInterval } = chatWorkflowRuns("chat-1");
		if (typeof refetchInterval !== "function") {
			throw new Error("refetchInterval must be a function");
		}
		const queryOf = (
			data: InfiniteData<WorkflowRun[], WorkflowRunPageParam> | undefined,
		) =>
			({ state: { data } }) as Parameters<
				NonNullable<typeof refetchInterval>
			>[0];
		const first = fullPage(49, new Array(50).fill("completed"));
		// The running run sits on page 2, so the fast interval has to come
		// from a page other than the first one.
		expect(
			refetchInterval(
				queryOf({
					pages: [first, fullPage(-1, ["completed", "running"])],
					pageParams: [{ after: "" }, { after: "c" }],
				}),
			),
		).toBe(5_000);
		expect(
			refetchInterval(
				queryOf({
					pages: [fullPage(49, ["completed", "running"]), first],
					pageParams: [{ after: "" }, { after: "c" }],
				}),
			),
		).toBe(5_000);
		expect(
			refetchInterval(
				queryOf({
					pages: [first, fullPage(-1, new Array(50).fill("completed"))],
					pageParams: [{ after: "" }, { after: "c" }],
				}),
			),
		).toBe(30_000);
		expect(refetchInterval(queryOf(undefined))).toBe(30_000);
	});
});
