import { QueryClient, type QueryKey, QueryObserver } from "react-query";
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	type Mock,
	vi,
} from "vitest";
import type {
	Chat,
	ChatWatchEvent,
	ChatWatchForkChange,
} from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	forkChatFamilyCapacity,
	forkChatFamilyCapacityKey,
} from "./forkChatFamily";
import {
	chatDescendantQuestions,
	chatDescendantQuestionsKey,
} from "./forkChatQuestions";
import { chatWakeups, chatWakeupsKey } from "./forkChatWakeups";
import { createForkChatWatch, forkWatchInvalidateGap } from "./forkChatWatch";
import {
	chatWorkflowRun,
	chatWorkflowRunKey,
	chatWorkflowRuns,
	chatWorkflowRunsKey,
} from "./forkWorkflows";

// The panel queries are inactive in these tests, so they must outlive the
// default gcTime of the shared test client.
const createTestQueryClient = (): QueryClient =>
	new QueryClient({
		defaultOptions: {
			queries: {
				retry: false,
				gcTime: Number.POSITIVE_INFINITY,
				refetchOnWindowFocus: false,
				networkMode: "offlineFirst",
			},
		},
	});

const mockRootChat: Chat = { ...MockChat, id: "root-1" };
const mockChildChat: Chat = {
	...MockChat,
	id: "child-1",
	parent_chat_id: "root-1",
	root_chat_id: "root-1",
};
const mockGrandchildChat: Chat = {
	...MockChat,
	id: "grandchild-1",
	parent_chat_id: "child-1",
	root_chat_id: "root-1",
};

// A fork_change event carries the zero chat; MockChat stands in for it.
const forkChange = (change: ChatWatchForkChange): ChatWatchEvent => ({
	kind: "fork_change",
	chat: MockChat,
	fork_change: change,
});

const childForkChange = (
	change: Omit<ChatWatchForkChange, "chat_id" | "root_chat_id">,
): ChatWatchEvent =>
	forkChange({ chat_id: "child-1", root_chat_id: "root-1", ...change });

// Every fork panel query of two families, so a test can tell which ones an
// event touched.
const panelKeys: Record<string, QueryKey> = {
	"root-1 questions": chatDescendantQuestionsKey("root-1"),
	"root-2 questions": chatDescendantQuestionsKey("root-2"),
	"root-1 family": forkChatFamilyCapacityKey("root-1"),
	"root-2 family": forkChatFamilyCapacityKey("root-2"),
	"child-1 wakeups": chatWakeupsKey("child-1"),
	"child-2 wakeups": chatWakeupsKey("child-2"),
	"child-1 runs": chatWorkflowRunsKey("child-1"),
	"child-1 run-1": chatWorkflowRunKey("child-1", "run-1"),
	"child-1 run-2": chatWorkflowRunKey("child-1", "run-2"),
	"child-2 runs": chatWorkflowRunsKey("child-2"),
};

const seedPanels = (queryClient: QueryClient) => {
	for (const key of Object.values(panelKeys)) {
		queryClient.setQueryData(key, []);
	}
};

const invalidatedPanels = (queryClient: QueryClient): string[] =>
	Object.entries(panelKeys)
		.filter(([, key]) => queryClient.getQueryState(key)?.isInvalidated)
		.map(([label]) => label)
		.sort();

describe("createForkChatWatch", () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it.each<{ name: string; event: ChatWatchEvent; expected: string[] }>([
		{
			name: "a questions change refetches only its root's questions",
			event: childForkChange({ facets: ["questions"] }),
			expected: ["root-1 questions"],
		},
		{
			name: "a wakeups change refetches only that chat's wake-ups",
			event: childForkChange({ facets: ["wakeups"] }),
			expected: ["child-1 wakeups"],
		},
		{
			name: "a workflows change refetches the run list and that run",
			event: childForkChange({
				facets: ["workflows"],
				workflow_run_id: "run-1",
			}),
			expected: ["child-1 run-1", "child-1 runs"],
		},
		{
			name: "a workflow_logs change refetches only that run",
			event: childForkChange({
				facets: ["workflow_logs"],
				workflow_run_id: "run-1",
			}),
			expected: ["child-1 run-1"],
		},
		{
			name: "a fork_change event without a payload touches nothing",
			event: { kind: "fork_change", chat: MockChat },
			expected: [],
		},
		{
			name: "a descendant status change refetches its family and questions",
			event: { kind: "status_change", chat: mockChildChat },
			expected: ["root-1 family", "root-1 questions"],
		},
		{
			name: "a nested descendant is keyed on its root",
			event: { kind: "created", chat: mockGrandchildChat },
			expected: ["root-1 family", "root-1 questions"],
		},
		{
			name: "a deleted descendant also refetches its wake-ups",
			event: { kind: "deleted", chat: mockChildChat },
			expected: ["child-1 wakeups", "root-1 family", "root-1 questions"],
		},
		{
			name: "a root's status change touches no fork panel",
			event: { kind: "status_change", chat: mockRootChat },
			expected: [],
		},
		{
			name: "a descendant title change touches no fork panel",
			event: { kind: "title_change", chat: mockChildChat },
			expected: [],
		},
	])("$name", ({ event, expected }) => {
		const queryClient = createTestQueryClient();
		seedPanels(queryClient);
		const watch = createForkChatWatch(queryClient);

		watch.onEvent(event);

		expect(invalidatedPanels(queryClient)).toEqual(expected);
		watch.dispose();
	});

	it("claims only fork_change events from the shared handler", () => {
		const watch = createForkChatWatch(createTestQueryClient());
		expect(watch.onEvent(childForkChange({ facets: ["wakeups"] }))).toBe(true);
		expect(watch.onEvent({ kind: "status_change", chat: mockChildChat })).toBe(
			false,
		);
		watch.dispose();
	});

	it("folds a burst of events into one leading and one trailing refetch", () => {
		const queryClient = createTestQueryClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const watch = createForkChatWatch(queryClient);
		const logs = childForkChange({
			facets: ["workflow_logs"],
			workflow_run_id: "run-1",
		});

		watch.onEvent(logs);
		expect(invalidate).toHaveBeenCalledTimes(1);
		for (let i = 0; i < 5; i++) {
			vi.advanceTimersByTime(500);
			watch.onEvent(logs);
		}
		expect(invalidate).toHaveBeenCalledTimes(1);

		vi.advanceTimersByTime(forkWatchInvalidateGap);
		expect(invalidate).toHaveBeenCalledTimes(2);
		expect(invalidate).toHaveBeenLastCalledWith({
			queryKey: chatWorkflowRunKey("child-1", "run-1"),
		});

		// A quiet gap ends the throttle, so the next event refetches at once.
		vi.advanceTimersByTime(forkWatchInvalidateGap);
		expect(invalidate).toHaveBeenCalledTimes(2);
		watch.onEvent(logs);
		expect(invalidate).toHaveBeenCalledTimes(3);
		watch.dispose();
	});

	it("throttles each query on its own", () => {
		const queryClient = createTestQueryClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const watch = createForkChatWatch(queryClient);

		for (const runId of ["run-1", "run-2"]) {
			watch.onEvent(
				childForkChange({
					facets: ["workflow_logs"],
					workflow_run_id: runId,
				}),
			);
		}

		expect(invalidate).toHaveBeenCalledTimes(2);
		watch.dispose();
	});

	it("drops the trailing refetch on dispose", () => {
		const queryClient = createTestQueryClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const watch = createForkChatWatch(queryClient);
		const wakeups = childForkChange({ facets: ["wakeups"] });

		watch.onEvent(wakeups);
		watch.onEvent(wakeups);
		watch.dispose();
		vi.advanceTimersByTime(forkWatchInvalidateGap * 2);

		expect(invalidate).toHaveBeenCalledTimes(1);
	});

	it("refetches every fork panel when the stream opens", () => {
		const queryClient = createTestQueryClient();
		seedPanels(queryClient);
		const watch = createForkChatWatch(queryClient);

		watch.onOpen();

		expect(invalidatedPanels(queryClient)).toEqual(
			Object.keys(panelKeys).sort(),
		);
		watch.dispose();
	});
});

describe("fork panel polling", () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	const runningRun = { status: "running" };

	type PanelCase = {
		name: string;
		// Mounts the panel query and returns its unsubscribe.
		mount: (queryClient: QueryClient, queryFn: Mock) => () => void;
		data: unknown;
		interval: number;
	};

	// Each panel used to poll every 3 to 30 seconds. Counts the requests a
	// mounted panel makes over the interval it polls at now.
	it.each<PanelCase>([
		{
			name: "descendant questions",
			mount: (queryClient, queryFn) =>
				new QueryObserver(queryClient, {
					...chatDescendantQuestions("root-1"),
					queryFn,
				}).subscribe(() => {}),
			data: [],
			interval: 60_000,
		},
		{
			name: "wake-ups",
			mount: (queryClient, queryFn) =>
				new QueryObserver(queryClient, {
					...chatWakeups("chat-1"),
					queryFn,
				}).subscribe(() => {}),
			data: [],
			interval: 60_000,
		},
		{
			name: "workflow runs with a running run",
			mount: (queryClient, queryFn) =>
				new QueryObserver(queryClient, {
					...chatWorkflowRuns("chat-1"),
					queryFn,
				}).subscribe(() => {}),
			data: [runningRun],
			interval: 60_000,
		},
		{
			name: "a running workflow run",
			mount: (queryClient, queryFn) =>
				new QueryObserver(queryClient, {
					...chatWorkflowRun("chat-1", "run-1"),
					queryFn,
				}).subscribe(() => {}),
			data: runningRun,
			interval: 60_000,
		},
		{
			name: "family capacity",
			mount: (queryClient, queryFn) =>
				new QueryObserver(queryClient, {
					...forkChatFamilyCapacity("root-1"),
					queryFn,
				}).subscribe(() => {}),
			data: {},
			interval: 30_000,
		},
	])(
		"polls $name only every $interval ms",
		async ({ mount, data, interval }) => {
			const queryFn = vi.fn().mockResolvedValue(data);
			const unsubscribe = mount(createTestQueryClient(), queryFn);
			try {
				await vi.advanceTimersByTimeAsync(0);
				expect(queryFn).toHaveBeenCalledTimes(1);

				await vi.advanceTimersByTimeAsync(interval - 1);
				expect(queryFn).toHaveBeenCalledTimes(1);

				await vi.advanceTimersByTimeAsync(1);
				expect(queryFn).toHaveBeenCalledTimes(2);
			} finally {
				unsubscribe();
			}
		},
	);
});
