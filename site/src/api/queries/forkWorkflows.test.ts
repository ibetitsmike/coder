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
	statuses.map((status, offset) => {
		const index = firstIndex - offset;
		// Timestamps step by 3 seconds per run, so a cursor names exactly
		// one run and a page's last run is the oldest one on it.
		return {
			...makeRun(index, status),
			created_at: stampOf(index),
		};
	});

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
		getRuns,
		pages: () => observer.getCurrentResult().data?.pages ?? [],
		refetch: () => observer.refetch(),
		fetchNextPage,
		start,
		unsubscribe: () => unsubscribe(),
	};
};

/** Waits for the observer to hold the given number of pages. */
const waitForPages = async (pages: () => readonly unknown[], count: number) => {
	await vi.waitFor(() => expect(pages()).toHaveLength(count));
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("chatWorkflowRuns polling", () => {
	// The observer resolves a refetch when its pages settle, so a tick's
	// later page fetch can still be in flight; flush it with a short real
	// timeout before reading the recorded calls.
	const settle = () => new Promise((resolve) => setTimeout(resolve, 50));

	it("makes one request per tick with two finished pages", async () => {
		const { getRuns, pages, fetchNextPage, refetch, start, unsubscribe } =
			observeRuns();
		try {
			// The list is newest first: run 49 is the oldest run of the
			// first page, and the cursor page holds the 50 runs behind it.
			const first = fullPage(49, Array(50).fill("completed"));
			const second = fullPage(-1, Array(50).fill("completed"));
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
			getRuns.mockClear();
			await refetch();
			await settle();
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
			const first = fullPage(49, Array(50).fill("completed"));
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
			// fresh. The running page is full, so its cursor survives.
			getRuns.mockResolvedValueOnce(first).mockResolvedValueOnce(runningPage);
			await refetch();
			await settle();
			expect(getRuns.mock.calls.slice(2).map((call) => call[1])).toEqual([
				"",
				cursor,
			]);
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
			const first = fullPage(49, Array(50).fill("completed"));
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
			await refetch();
			await settle();
			expect(
				pages()[1].some((run: WorkflowRun) => run.status === "running"),
			).toBe(true);
			expect(getRuns.mock.calls.slice(2).map((call) => call[1])).toEqual([
				"",
				cursor,
			]);
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
			const first = fullPage(49, Array(50).fill("completed"));
			const cursor = cursorOf(first);
			getRuns
				.mockResolvedValueOnce(first)
				.mockResolvedValueOnce(fullPage(-1, Array(50).fill("completed")))
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
							? fullPage(0, Array(50).fill("completed"))
							: [],
				),
			);
			await refetch();
			await settle();
			expect(getRuns.mock.calls.slice(2).map((call) => call[1])).toEqual([
				"",
				newCursor,
			]);
		} finally {
			unsubscribe();
		}
	});
});

describe("runListHasRunningRun", () => {
	it("sees a running run on any loaded page, not just the first", () => {
		const first = fullPage(49, Array(50).fill("completed"));
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
				pages: [first, fullPage(-1, Array(50).fill("completed"))],
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
		const first = fullPage(49, Array(50).fill("completed"));
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
					pages: [first, fullPage(-1, Array(50).fill("completed"))],
					pageParams: [{ after: "" }, { after: "c" }],
				}),
			),
		).toBe(30_000);
		expect(refetchInterval(queryOf(undefined))).toBe(30_000);
	});
});
