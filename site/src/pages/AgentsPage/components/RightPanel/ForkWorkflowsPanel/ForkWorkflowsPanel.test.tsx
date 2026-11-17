import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	focusManager,
	type InfiniteData,
	InfiniteQueryObserver,
	QueryClientProvider,
} from "react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { describe, expect, it, vi } from "vitest";
import * as forkWorkflowsApi from "#/api/forkWorkflows";
import {
	chatWorkflowRuns,
	chatWorkflowRunsKey,
} from "#/api/queries/forkWorkflows";
import type { WorkflowRun, WorkflowRunDetail } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockChatWorkflowRun,
	MockChatWorkflowRunDetail,
	seedChatWriteAccess,
} from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { generateUUID } from "#/utils/random";
import { ForkWorkflowsPanel } from "./ForkWorkflowsPanel";

// The panel remembers the open run per chat, so each test uses its own chat.
const renderPanel = (chatId: string) => {
	const chatPath = `/agents/${chatId}`;
	const router = createMemoryRouter(
		[
			{
				path: chatPath,
				element: <ForkWorkflowsPanel chatId={chatId} />,
			},
			{ path: "/agents/:chatId", element: null },
		],
		{ initialEntries: [chatPath] },
	);
	const queryClient = createTestQueryClient();
	// The seeds stand in for the chat page's cache. The test client's gcTime
	// is 0, so the helper's infinite staleness opts out of cache eviction: the
	// run detail only observes them once the user opens a run.
	seedChatWriteAccess(queryClient, chatId, true);
	renderComponent(
		<QueryClientProvider client={queryClient}>
			<RouterProvider router={router} />
		</QueryClientProvider>,
	);
	return { router, queryClient };
};

describe("ForkWorkflowsPanel", () => {
	it("stops a running run after the user confirms", async () => {
		const chatId = generateUUID();
		let run: WorkflowRunDetail = {
			...MockChatWorkflowRunDetail,
			chat_id: chatId,
			status: "running",
			result: undefined,
			finished_at: undefined,
		};
		// The list holds a running run, so the detail fetch loads with
		// it; the stop does not have to.
		const getRuns = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRuns")
			.mockResolvedValue([{ ...MockChatWorkflowRun, status: "running" }]);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockImplementation(
			async () => run,
		);
		const stopRun = vi
			.spyOn(forkWorkflowsApi, "stopChatWorkflowRun")
			.mockImplementation(async () => {
				run = { ...run, status: "stopped", error: "stopped by user" };
				return run;
			});
		renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		await user.click(
			await screen.findByRole("button", { name: "Stop workflow run" }),
		);
		expect(stopRun).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		expect(stopRun).toHaveBeenCalledWith(chatId, MockChatWorkflowRunDetail.id);
		await waitFor(() => expect(getRuns).toHaveBeenCalledTimes(2));
		expect(
			screen.queryByRole("button", { name: "Stop workflow run" }),
		).not.toBeInTheDocument();
		expect(
			screen.getByRole("heading", { name: /review changed files/i }),
		).toHaveFocus();
	});

	it("reports a failed stop and refreshes the runs", async () => {
		const chatId = generateUUID();
		const runningRun: WorkflowRunDetail = {
			...MockChatWorkflowRunDetail,
			chat_id: chatId,
			status: "running",
			result: undefined,
			finished_at: undefined,
		};
		const getRuns = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRuns")
			.mockResolvedValue([{ ...MockChatWorkflowRun, status: "running" }]);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			runningRun,
		);
		vi.spyOn(forkWorkflowsApi, "stopChatWorkflowRun").mockRejectedValue(
			mockApiError({ message: "The workflow run already ended." }),
		);
		const toastError = vi.spyOn(toast, "error");
		renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		await user.click(
			await screen.findByRole("button", { name: "Stop workflow run" }),
		);
		await user.click(screen.getByRole("button", { name: "Stop run" }));

		await waitFor(() =>
			expect(toastError).toHaveBeenCalledWith(
				"The workflow run already ended.",
				expect.anything(),
			),
		);
		// The run may have ended on its own, so the list refreshes anyway.
		await waitFor(() => expect(getRuns).toHaveBeenCalledTimes(2));
	});

	it("loads the older page with the last run's cursor", async () => {
		const chatId = generateUUID();
		// A full page promises older runs; the cursor page holds one.
		const firstPage = Array.from({ length: 50 }, (_, index) => ({
			...MockChatWorkflowRun,
			id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
			created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
		}));
		const getRuns = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRuns")
			.mockImplementation((_chatId, after) =>
				Promise.resolve(after ? [MockChatWorkflowRun] : firstPage),
			);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			MockChatWorkflowRunDetail,
		);
		const { queryClient } = renderPanel(chatId);

		await screen.findByRole("list", { name: "Workflow runs" });
		// The cursor page asks for the runs behind the last run of the
		// first page, as the server's after parameter expects.
		const last = firstPage[firstPage.length - 1];
		await userEvent
			.setup()
			.click(await screen.findByRole("button", { name: "Load older runs" }));
		await waitFor(() =>
			expect(getRuns).toHaveBeenCalledWith(
				chatId,
				`${last.created_at}/${last.id}`,
				expect.anything(),
			),
		);
		// The query holds both pages: the first page's params plus the
		// cursor page behind the last run of that page.
		await waitFor(() => {
			const cached = queryClient.getQueryData<
				InfiniteData<WorkflowRun[], { after: string }>
			>(chatWorkflowRunsKey(chatId));
			expect(cached?.pageParams).toEqual([
				{ after: "" },
				{ after: `${last.created_at}/${last.id}` },
			]);
			expect(cached?.pages).toHaveLength(2);
		});
	});

	it("offers no older page after a short page", async () => {
		const chatId = generateUUID();
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockResolvedValue([
			MockChatWorkflowRun,
		]);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			MockChatWorkflowRunDetail,
		);
		const { queryClient } = renderPanel(chatId);

		await screen.findByRole("list", { name: "Workflow runs" });
		// The query itself ends the paging: one page's params and no next
		// cursor, so a later refresh cannot ask for a page the server never
		// promised.
		await waitFor(() => {
			const cached = queryClient.getQueryData<
				InfiniteData<WorkflowRun[], { after: string }>
			>(chatWorkflowRunsKey(chatId));
			expect(cached?.pageParams).toEqual([{ after: "" }]);
			expect(cached?.pages).toHaveLength(1);
		});
		await waitFor(() => {
			// hasNextPage is false exactly when getNextPageParam returns
			// undefined for the short page.
			const observer = new InfiniteQueryObserver(
				queryClient,
				chatWorkflowRuns(chatId),
			);
			expect(observer.getCurrentResult().hasNextPage).toBe(false);
		});
	});

	it("keeps focus on the list when the last page removes the control", async () => {
		const chatId = generateUUID();
		const firstPage = Array.from({ length: 50 }, (_, index) => ({
			...MockChatWorkflowRun,
			id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
			created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
		}));
		// The cursor page is short, so loading it ends the paging and
		// unmounts "Load older runs" while the user holds focus on it.
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockImplementation(
			(_chatId, after) =>
				Promise.resolve(after ? [MockChatWorkflowRun] : firstPage),
		);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			MockChatWorkflowRunDetail,
		);
		renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: "Load older runs" }),
		);
		await screen.findByRole("list", { name: "Workflow runs" });
		// The focus moved from the unmounted button to the list, not to the
		// body, so keyboard users keep their place.
		await waitFor(() =>
			expect(screen.getByRole("list", { name: "Workflow runs" })).toHaveFocus(),
		);
	});

	it("leaves focus alone when the user moved it off the removed control", async () => {
		const chatId = generateUUID();
		const firstPage = Array.from({ length: 50 }, (_, index) => ({
			...MockChatWorkflowRun,
			id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
			created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
		}));
		// The cursor page is short, so loading it ends the paging and
		// unmounts "Load older runs" after the user moved focus to a run.
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockImplementation(
			(_chatId, after) =>
				Promise.resolve(after ? [MockChatWorkflowRun] : firstPage),
		);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			MockChatWorkflowRunDetail,
		);
		renderPanel(chatId);

		const control = await screen.findByRole("button", {
			name: "Load older runs",
		});
		// While the page loaded, the user moved focus to a spot that is
		// not focusable, so it fell to the body and the button no longer
		// owns it. jsdom does not fire blur on unmount, so move focus off
		// the control by hand while it still owns it.
		control.focus();
		expect(control).toHaveFocus();
		(control as HTMLElement).blur();

		// The cursor page is short, so the control unmounts once it
		// resolves. No rerender happened while the test waited, so give
		// React the interval ticks it needs to settle the result into the
		// DOM.
		await waitFor(() => {
			expect(
				screen.queryByText("Load older runs", { selector: "span" }),
			).toBeNull();
		});
		// The list must not pull focus back: the user left it somewhere
		// on purpose, and the body is where a removed focused element
		// naturally lands.
		expect(
			screen.getByRole("list", { name: "Workflow runs" }),
		).not.toHaveFocus();
	});

	it("keeps the older-runs control focused while a fetch is in flight", async () => {
		const chatId = generateUUID();
		// The test client's refetchInterval would wait out the real tick, so
		// the test drives both fetches itself and holds each one open.
		focusManager.setFocused(false);
		try {
			// Full pages keep an older page promised, so the control stays
			// mounted through both fetches.
			const firstPage = Array.from({ length: 50 }, (_, index) => ({
				...MockChatWorkflowRun,
				id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
				created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
			}));
			const olderPage = Array.from({ length: 50 }, (_, index) => ({
				...MockChatWorkflowRun,
				id: `00000001-0000-4000-8000-${String(index).padStart(12, "0")}`,
				created_at: `2026-11-11T10:00:${String(index).padStart(2, "0")}Z`,
			}));
			// The fetch in flight holds this resolver until the test lets it
			// finish, so the control's states can be asserted mid-fetch.
			const held: { resolve: (() => void) | null } = { resolve: null };
			const hold = (value: WorkflowRun[]) =>
				new Promise<WorkflowRun[]>((resolve) => {
					held.resolve = () => resolve(value);
				});
			let phase: "next-page" | "tick" | "settled" = "next-page";
			vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockImplementation(
				(_chatId, after) => {
					if (phase === "next-page") {
						return after ? hold(olderPage) : Promise.resolve(firstPage);
					}
					if (phase === "tick") {
						// A tick reuses the finished older page from the cache,
						// so holding the first page holds the whole tick.
						return after ? Promise.resolve(olderPage) : hold(firstPage);
					}
					return Promise.resolve(after ? olderPage : firstPage);
				},
			);
			vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
				MockChatWorkflowRunDetail,
			);
			const { queryClient } = renderPanel(chatId);
			const user = userEvent.setup();
			const getRuns = vi.mocked(forkWorkflowsApi.getChatWorkflowRuns);
			const releaseHeld = () => {
				const release = held.resolve;
				phase = "settled";
				if (release) {
					release();
				}
			};

			let control = await screen.findByRole("button", {
				name: "Load older runs",
			});
			await user.click(control);

			// A next-page fetch is in flight. The control must stay
			// operable, keep focus and refuse a second load while it runs.
			await waitFor(() => expect(getRuns).toHaveBeenCalledTimes(2));
			expect(control).not.toBeDisabled();
			expect(control).toHaveAccessibleName("Load older runs");
			control.focus();
			expect(control).toHaveFocus();
			await user.click(control);
			expect(getRuns).toHaveBeenCalledTimes(2);
			releaseHeld();
			// The fetch's answer lands in the cache before the tick starts.
			await waitFor(() =>
				expect(queryClient.getQueryData(chatWorkflowRunsKey(chatId))).toEqual({
					pages: [firstPage, olderPage],
					pageParams: [{ after: "" }, expect.anything()],
				}),
			);

			// A background tick is in flight: the same story.
			control = screen.getByRole("button", { name: "Load older runs" });
			// Move focus away first, so re-focusing the control is not a
			// no-op on an element that already holds focus: a disabled
			// control would then fail to take focus back.
			const runList = screen.getByRole("list", { name: "Workflow runs" });
			runList.focus();
			expect(runList).toHaveFocus();
			phase = "tick";
			queryClient.refetchQueries({ queryKey: chatWorkflowRunsKey(chatId) });
			await waitFor(() => expect(getRuns).toHaveBeenCalledTimes(3));
			expect(control).not.toBeDisabled();
			expect(control).toHaveAccessibleName("Load older runs");
			control.focus();
			expect(control).toHaveFocus();
			await user.click(control);
			expect(getRuns).toHaveBeenCalledTimes(3);
			releaseHeld();
			// The tick is over only when its last request has settled.
			await waitFor(() => expect(queryClient.isFetching()).toBe(0));
		} finally {
			focusManager.setFocused(true);
		}
	});

	it("opens a run, then a step's child chat, and back again", async () => {
		const chatId = generateUUID();
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockResolvedValue([
			MockChatWorkflowRun,
		]);
		const getRun = vi
			.spyOn(forkWorkflowsApi, "getChatWorkflowRun")
			.mockResolvedValue(MockChatWorkflowRunDetail);
		const { router } = renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		expect(getRun).toHaveBeenCalledWith(
			chatId,
			MockChatWorkflowRun.id,
			expect.anything(),
		);
		await user.click(
			await screen.findByRole("link", { name: /coderd\/users\.go/ }),
		);
		expect(router.state.location.pathname).toBe(
			`/agents/${MockChatWorkflowRunDetail.steps[1].id}`,
		);

		// Browser back remounts the panel on the run that was open.
		await router.navigate(-1);
		await screen.findByRole("button", { name: "All runs" });
	});
});
