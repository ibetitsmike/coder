// This file is fork code: the polling behavior of the workflow run list
// query (fork-workflows experiment). A refetch walks every loaded page, so
// these tests pin that steady state costs one request per tick and that a
// running run or a shifted cursor still refreshes the pages behind it.

import { InfiniteQueryObserver, QueryClient } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import type { WorkflowRun } from "#/api/typesGenerated";
import { MockChatWorkflowRun } from "#/testHelpers/forkWorkflows";
import { chatWorkflowRuns } from "./forkWorkflows";

const makeRun = (index: number, status: string): WorkflowRun => ({
	...MockChatWorkflowRun,
	id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
	status,
	created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
});

const fullPage = (statuses: readonly string[]) =>
	statuses.map((status, index) => makeRun(index, status));

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

	it("makes one request per tick with two loaded pages and nothing running", async () => {
		const { getRuns, pages, fetchNextPage, refetch, start, unsubscribe } =
			observeRuns();
		try {
			// First load: a full page, then a short page behind it. The
			// second fetch walks the cursor the first page produced.
			getRuns
				.mockResolvedValueOnce(fullPage(Array(50).fill("completed")))
				.mockResolvedValueOnce([makeRun(2, "completed")]);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);

			const callsAfterLoad = getRuns.mock.calls.length;
			// Two ticks at the slow interval: a completed older page is
			// reused from the cache, so only the first page is fetched.
			await refetch();
			await refetch();
			expect(getRuns.mock.calls.length).toBe(callsAfterLoad + 2);
			expect(getRuns.mock.calls[callsAfterLoad][1]).toBe("");
			// Every tick fetched only the first page: the older page is
			// reused from the cache, and its cursor is absent from the calls.
			for (const call of getRuns.mock.calls.slice(callsAfterLoad)) {
				expect(call[1]).toBe("");
			}
		} finally {
			unsubscribe();
		}
	});

	it("refetches a page with a running run at the fast interval", async () => {
		const { getRuns, pages, fetchNextPage, refetch, start, unsubscribe } =
			observeRuns();
		try {
			// The older page holds the running run. The first page's cursor
			// is the address of that page.
			const first = fullPage(Array(50).fill("completed"));
			const cursor = cursorOf(first);
			getRuns
				.mockResolvedValueOnce(first)
				.mockResolvedValueOnce([makeRun(2, "running")]);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);
			// Every tick fetches the running page; the finished first page is
			// fetched too, since the first page is always fetched fresh.
			getRuns
				.mockResolvedValueOnce(first)
				.mockResolvedValueOnce([makeRun(2, "running")]);
			await refetch();
			await settle();
			expect(getRuns.mock.calls.slice(2).map((call) => call[1])).toEqual([
				"",
				cursor,
			]);
			getRuns
				.mockResolvedValueOnce(first)
				.mockResolvedValueOnce([makeRun(2, "running")]);
			await refetch();
			await settle();
			expect(getRuns.mock.calls.slice(4).map((call) => call[1])).toEqual([
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
			// A new run lands on the first page, so every older cursor moves.
			const base = fullPage(Array(50).fill("completed"));
			const loadFirst = [makeRun(0, "completed"), ...base];
			getRuns
				.mockResolvedValueOnce(loadFirst)
				.mockResolvedValueOnce([makeRun(2, "completed")]);
			start();
			await waitForPages(pages, 1);
			await fetchNextPage();
			await waitForPages(pages, 2);

			const oldCursor = cursorOf(base);
			expect(getRuns.mock.calls[1][1]).toBe(oldCursor);

			// The tick fetches the first page, which now ends with a newer
			// run and shifts the cursor, so the page behind it is fetched
			// with the new cursor, not reused from the cache. The older page
			// holds the run that moved off the first page.
			const shiftedFirst = [...loadFirst, makeRun(51, "completed")];
			getRuns.mockImplementation((_chatId, after) =>
				Promise.resolve(
					after === "" ? shiftedFirst : [makeRun(0, "completed")],
				),
			);
			await refetch();
			await settle();
			const tickCursors = getRuns.mock.calls.slice(2).map((call) => call[1]);
			// The cursor moved with the first page, so the page behind it was
			// fetched with the new cursor, not reused from the cache.
			expect(tickCursors).toHaveLength(2);
			expect(tickCursors[0]).toBe("");
			expect(tickCursors[1]).not.toBe(oldCursor);
			expect(tickCursors[1]).toBe(cursorOf(shiftedFirst));
		} finally {
			unsubscribe();
		}
	});
});
