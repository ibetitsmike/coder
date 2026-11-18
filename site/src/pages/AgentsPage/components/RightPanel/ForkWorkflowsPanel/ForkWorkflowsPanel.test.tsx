import { screen, waitFor, within } from "@testing-library/react";
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

	it("announces appended log lines while the run is live", async () => {
		const chatId = generateUUID();
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockResolvedValue([
			{ ...MockChatWorkflowRun, status: "running" },
		]);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue({
			...MockChatWorkflowRunDetail,
			chat_id: chatId,
			status: "running",
			result: undefined,
			finished_at: undefined,
		});
		renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		const logs = await screen.findByRole("log", { name: "Logs" });
		expect(logs).toHaveAttribute("aria-live", "polite");
		// aria-relevant stays unset on purpose: role="log" defaults to
		// "additions text", and the appended text node counts as a text
		// change, which an additions-only value would suppress.
		expect(logs).not.toHaveAttribute("aria-relevant");
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
				50,
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

	it("keeps focus moving through the runs when the last page removes the control", async () => {
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
		// The focus moved from the unmounted button onto the first run of
		// the appended page, the one after the last run the user could
		// see before, so keyboard focus does not drop to the body.
		await waitFor(() =>
			expect(
				within(screen.getAllByRole("listitem")[50]).getByRole("button"),
			).toHaveFocus(),
		);
	});

	it("lands focus on the last run when the final page is empty", async () => {
		const chatId = generateUUID();
		const firstPage = Array.from({ length: 50 }, (_, index) => ({
			...MockChatWorkflowRun,
			id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
			created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
		}));
		// A total that is an exact multiple of the page size gives the last
		// fetch an empty page with no next cursor, so no new row follows
		// the one the user was reading.
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockImplementation(
			(_chatId, after) =>
				Promise.resolve(
					after === `${firstPage[49].created_at}/${firstPage[49].id}`
						? []
						: after
							? [MockChatWorkflowRun]
							: firstPage,
				),
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
		// The empty page leaves the previously last run as the last one, so
		// that row's button takes focus instead of the list.
		await waitFor(() =>
			expect(
				within(screen.getAllByRole("listitem")[49]).getByRole("button", {
					name: /review changed files/i,
				}),
			).toHaveFocus(),
		);
	});

	it("keeps focus moving through three pages of runs", async () => {
		const chatId = generateUUID();
		const firstPage = Array.from({ length: 50 }, (_, index) => ({
			...MockChatWorkflowRun,
			id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
			created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
		}));
		const secondPage = Array.from({ length: 50 }, (_, index) => ({
			...MockChatWorkflowRun,
			id: `00000001-0000-4000-8000-${String(index).padStart(12, "0")}`,
			created_at: `2026-11-11T10:00:${String(index).padStart(2, "0")}Z`,
		}));
		// The first two pages are full and the third is short, so the
		// control unmounts only after the second load ends the paging.
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockImplementation(
			(_chatId, after) =>
				Promise.resolve(
					after === `${secondPage[49].created_at}/${secondPage[49].id}`
						? [MockChatWorkflowRun]
						: after
							? secondPage
							: firstPage,
				),
		);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			MockChatWorkflowRunDetail,
		);
		renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: "Load older runs" }),
		);
		await user.click(
			await screen.findByRole("button", { name: "Load older runs" }),
		);
		await screen.findByRole("list", { name: "Workflow runs" });
		await waitFor(() =>
			expect(
				within(screen.getAllByRole("listitem")[100]).getByRole("button"),
			).toHaveFocus(),
		);
	});

	it("leaves the restored focus alone through later polls", async () => {
		const chatId = generateUUID();
		const firstPage = Array.from({ length: 50 }, (_, index) => ({
			...MockChatWorkflowRun,
			id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
			created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
		}));
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockImplementation(
			(_chatId, after) =>
				Promise.resolve(after ? [MockChatWorkflowRun] : firstPage),
		);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			MockChatWorkflowRunDetail,
		);
		const { queryClient } = renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: "Load older runs" }),
		);
		await waitFor(() =>
			expect(
				within(screen.getAllByRole("listitem")[50]).getByRole("button"),
			).toHaveFocus(),
		);
		const restored = within(screen.getAllByRole("listitem")[50]).getByRole(
			"button",
		);
		expect(restored).toHaveFocus();

		// The user moves focus to an earlier run, then the list polls; the
		// restore must not pull focus back onto the restored run. Focus
		// moves without a click, because a click would open that run.
		const earlier = within(screen.getAllByRole("listitem")[0]).getByRole(
			"button",
		);
		earlier.focus();
		expect(earlier).toHaveFocus();
		await queryClient.refetchQueries({
			queryKey: chatWorkflowRunsKey(chatId),
		});
		await waitFor(() => expect(queryClient.isFetching()).toBe(0));
		expect(
			within(screen.getAllByRole("listitem")[0]).getByRole("button"),
		).toHaveFocus();
	});

	it("leaves focus alone when the user moved it off the removed control", async () => {
		const chatId = generateUUID();
		const firstPage = Array.from({ length: 50 }, (_, index) => ({
			...MockChatWorkflowRun,
			id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
			created_at: `2026-11-12T10:00:${String(index).padStart(2, "0")}Z`,
		}));
		// The cursor page is held open, so the click starts the request
		// and focus can move before the short page ends the paging and
		// unmounts "Load older runs".
		const cursorResolver: { current?: () => void } = {};
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockImplementation(
			(_chatId, after) =>
				after
					? new Promise<WorkflowRun[]>((resolve) => {
							cursorResolver.current = () => resolve([MockChatWorkflowRun]);
						})
					: Promise.resolve(firstPage),
		);
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue(
			MockChatWorkflowRunDetail,
		);
		renderPanel(chatId);
		const user = userEvent.setup();
		const getRuns = vi.mocked(forkWorkflowsApi.getChatWorkflowRuns);

		const control = await screen.findByRole("button", {
			name: "Load older runs",
		});
		await user.click(control);
		await waitFor(() => expect(getRuns).toHaveBeenCalledTimes(2));
		// While the cursor page is pending, the user moves focus off the
		// control, and jsdom drops it to the body.
		await user.click(document.body);
		expect(control).not.toHaveFocus();

		cursorResolver.current?.();
		// The cursor page is short, so the control unmounts once it
		// resolves.
		await waitFor(() => {
			expect(
				screen.queryByRole("button", { name: "Load older runs" }),
			).toBeNull();
		});
		// The list must not pull focus back: the user left it on the body
		// on purpose, and the body is where a removed focused element
		// naturally lands.
		expect(
			screen.getByRole("list", { name: "Workflow runs" }),
		).not.toHaveFocus();
		expect(document.body).toHaveFocus();
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

	it("shows a large integer in Arguments exactly as the server sent it", async () => {
		const chatId = generateUUID();
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRuns").mockResolvedValue([
			MockChatWorkflowRun,
		]);
		// The axios default parse would round 9007199254740993 to
		// ...992 before the formatter saw it, so this pins the lossless
		// path end to end, from the response text to the rendered block.
		vi.spyOn(forkWorkflowsApi, "getChatWorkflowRun").mockResolvedValue({
			...MockChatWorkflowRunDetail,
			chat_id: chatId,
			args: { count: { forkUnsafeInteger: "9007199254740993" } },
		});
		renderPanel(chatId);
		const user = userEvent.setup();

		await user.click(
			await screen.findByRole("button", { name: /review changed files/i }),
		);
		const argumentsBlock = await screen.findByRole("region", {
			name: "Arguments",
		});
		expect(argumentsBlock).toHaveTextContent("9007199254740993");
		expect(argumentsBlock).not.toHaveTextContent("9007199254740992");
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
