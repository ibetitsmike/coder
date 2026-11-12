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

export const MockForkWorkflowArgs = { files: ["coderd/chats.go", "coderd/users.go"] };

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
