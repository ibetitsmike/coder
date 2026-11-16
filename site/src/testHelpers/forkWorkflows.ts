import type { WorkflowRun, WorkflowRunDetail } from "#/api/typesGenerated";

export const MockForkWorkflowRunId = "3f6d2c1a-9b4e-4f0a-8c7d-1e2f3a4b5c6d";

export const MockForkWorkflowScript = `def main(args):
    handles = [
        spawn("Review %s for bugs" % path, type = "explore", title = path)
        for path in args["files"]
    ]
    reviews = wait(handles)
    log("%d reviews" % len(reviews))
    return agent("Summarize these reviews: %s" % json.encode(reviews), title = "Summary")
`;

export const MockForkWorkflowArgs = {
	files: ["coderd/chats.go", "coderd/users.go"],
};

export const MockForkWorkflowStartedResult = {
	run_id: MockForkWorkflowRunId,
	status: "running",
	steps: [],
};

export const MockForkWorkflowRunningResult = {
	run_id: MockForkWorkflowRunId,
	status: "running",
	phases: ["review", "summarize"],
	current_phase: "summarize",
	steps: [
		{
			chat_id: "aa111111-1111-4111-8111-111111111111",
			title: "coderd/chats.go",
			status: "reported",
			phase: "review",
		},
		{
			chat_id: "bb222222-2222-4222-8222-222222222222",
			title: "coderd/users.go",
			status: "reminded",
			phase: "review",
		},
	],
};

export const MockForkWorkflowCompletedResult = {
	run_id: MockForkWorkflowRunId,
	status: "completed",
	phases: ["review", "summarize"],
	current_phase: "summarize",
	result: {
		text: Array.from(
			{ length: 30 },
			(_, index) =>
				`Finding ${index + 1}: the handler writes to a nil map when the request has no labels.`,
		).join("\n"),
	},
	steps: [
		{
			chat_id: "aa111111-1111-4111-8111-111111111111",
			title: "coderd/chats.go",
			status: "reported",
			reused: true,
			phase: "review",
		},
		{
			chat_id: "bb222222-2222-4222-8222-222222222222",
			title: "coderd/users.go",
			status: "reported",
			phase: "review",
		},
		{
			chat_id: "cc333333-3333-4333-8333-333333333333",
			title: "Summary",
			status: "reported",
			phase: "summarize",
		},
	],
	logs: ["2 reviews"],
};

export const MockForkWorkflowFailedResult = {
	run_id: MockForkWorkflowRunId,
	status: "failed",
	phases: ["review"],
	current_phase: "review",
	error:
		'step "coderd/users.go" failed: the step ended its turn twice without calling workflow_report',
	steps: [
		{
			chat_id: "aa111111-1111-4111-8111-111111111111",
			title: "coderd/chats.go",
			status: "reported",
			phase: "review",
		},
		{
			chat_id: "bb222222-2222-4222-8222-222222222222",
			title: "coderd/users.go",
			status: "failed",
			error: "the step ended its turn twice without calling workflow_report",
			phase: "review",
		},
	],
};

/** A fixed "now" a few minutes after the mock runs start. */
export const MockForkWorkflowNow = Date.parse("2026-11-12T10:05:00Z");

export const MockForkWorkflowChatId = "dd444444-4444-4444-8444-444444444444";

export const MockChatWorkflowRunDetail: WorkflowRunDetail = {
	id: MockForkWorkflowRunId,
	chat_id: MockForkWorkflowChatId,
	status: "completed",
	title: "Review changed files",
	error: "",
	result: MockForkWorkflowCompletedResult.result,
	created_at: "2026-11-12T10:00:00Z",
	updated_at: "2026-11-12T10:03:20Z",
	finished_at: "2026-11-12T10:03:20Z",
	script: MockForkWorkflowScript,
	logs: ["2 reviews"],
	phases: ["review", "summarize"],
	current_phase: "summarize",
	steps: MockForkWorkflowCompletedResult.steps.map((step, ordinal) => ({
		id: step.chat_id,
		run_id: MockForkWorkflowRunId,
		title: step.title,
		status: step.status,
		reused: step.reused === true,
		error: "",
		ordinal,
		phase: step.phase,
		started_at: "2026-11-12T10:00:05Z",
		finished_at: "2026-11-12T10:03:00Z",
	})),
};

export const MockChatWorkflowRun: WorkflowRun = {
	id: MockChatWorkflowRunDetail.id,
	chat_id: MockChatWorkflowRunDetail.chat_id,
	status: MockChatWorkflowRunDetail.status,
	title: MockChatWorkflowRunDetail.title,
	error: MockChatWorkflowRunDetail.error,
	created_at: MockChatWorkflowRunDetail.created_at,
	updated_at: MockChatWorkflowRunDetail.updated_at,
	finished_at: MockChatWorkflowRunDetail.finished_at,
	script_preview: MockForkWorkflowScript.slice(0, 200),
	phases: MockChatWorkflowRunDetail.phases,
	current_phase: MockChatWorkflowRunDetail.current_phase,
	step_counts: { total: 3, running: 0, done: 3, failed: 0 },
};

import type { QueryClient } from "react-query";
import { getAuthorizationKey } from "#/api/queries/authCheck";
import { chatEntityKey } from "#/api/queries/chats";
import { MockChat } from "./chatEntities";

/**
 * Seeds the chat and its update check into the cache, the way the chat page
 * holds them. The seeds opt out of cache eviction, because the test client's
 * gcTime is 0 and the view's queries must still observe them.
 */
export const seedChatWriteAccess = (
	queryClient: QueryClient,
	chatId: string,
	canUpdateChat: boolean,
	options: { archived?: boolean; planMode?: boolean } = {},
): void => {
	const seed = (key: readonly unknown[], data: unknown) => {
		queryClient.setQueryDefaults(key, {
			staleTime: Number.POSITIVE_INFINITY,
			gcTime: Number.POSITIVE_INFINITY,
		});
		queryClient.setQueryData(key, data);
	};
	seed(chatEntityKey(chatId), {
		...MockChat,
		id: chatId,
		archived: options.archived ?? false,
		plan_mode: options.planMode ? "plan" : undefined,
	});
	seed(
		getAuthorizationKey({
			checks: {
				canUpdateChat: {
					object: {
						resource_type: "chat",
						owner_id: MockChat.owner_id,
						organization_id: MockChat.organization_id,
					},
					action: "update",
				},
			},
		}),
		{ canUpdateChat },
	);
};
