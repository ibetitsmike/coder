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
	steps: [
		{
			chat_id: "aa111111-1111-4111-8111-111111111111",
			title: "coderd/chats.go",
			status: "reported",
		},
		{
			chat_id: "bb222222-2222-4222-8222-222222222222",
			title: "coderd/users.go",
			status: "reminded",
		},
	],
};

export const MockForkWorkflowCompletedResult = {
	run_id: MockForkWorkflowRunId,
	status: "completed",
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
		},
		{
			chat_id: "bb222222-2222-4222-8222-222222222222",
			title: "coderd/users.go",
			status: "reported",
		},
		{
			chat_id: "cc333333-3333-4333-8333-333333333333",
			title: "Summary",
			status: "reported",
		},
	],
	logs: ["2 reviews"],
};

export const MockForkWorkflowFailedResult = {
	run_id: MockForkWorkflowRunId,
	status: "failed",
	error:
		'step "coderd/users.go" failed: the step ended its turn twice without calling workflow_report',
	steps: [
		{
			chat_id: "aa111111-1111-4111-8111-111111111111",
			title: "coderd/chats.go",
			status: "reported",
		},
		{
			chat_id: "bb222222-2222-4222-8222-222222222222",
			title: "coderd/users.go",
			status: "failed",
			error: "the step ended its turn twice without calling workflow_report",
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
	steps: MockForkWorkflowCompletedResult.steps.map((step, ordinal) => ({
		id: step.chat_id,
		run_id: MockForkWorkflowRunId,
		title: step.title,
		status: step.status,
		reused: step.reused === true,
		error: "",
		ordinal,
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
	step_counts: { total: 3, running: 0, done: 3, failed: 0 },
};
