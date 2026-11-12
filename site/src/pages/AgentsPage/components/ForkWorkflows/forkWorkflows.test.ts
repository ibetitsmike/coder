import { describe, expect, it } from "vitest";
import {
	MockForkWorkflowCompletedResult,
	MockForkWorkflowRunId,
} from "#/testHelpers/forkWorkflows";
import {
	forkWorkflowRunStatusMeta,
	forkWorkflowStepProgress,
	parseForkWorkflowRunSnapshot,
} from "./forkWorkflows";

describe("parseForkWorkflowRunSnapshot", () => {
	it("parses the JSON text that the tools return", () => {
		const snapshot = parseForkWorkflowRunSnapshot(
			JSON.stringify(MockForkWorkflowCompletedResult),
		);
		expect(snapshot).toEqual({
			runId: MockForkWorkflowRunId,
			status: "completed",
			result: MockForkWorkflowCompletedResult.result,
			error: "",
			steps: MockForkWorkflowCompletedResult.steps.map((step) => ({
				chatId: step.chat_id,
				title: step.title,
				status: step.status,
				reused: step.reused === true,
				error: "",
			})),
			logs: ["2 reviews"],
		});
	});

	it.each([
		["plain text", "the run is not found"],
		["no run_id", { status: "running", steps: [] }],
		["a step without chat_id", { run_id: "r", status: "running", steps: [{}] }],
	])("rejects %s", (_, value) => {
		expect(parseForkWorkflowRunSnapshot(value)).toBeNull();
	});
});

describe("forkWorkflowStepProgress", () => {
	it("counts reported steps", () => {
		const snapshot = parseForkWorkflowRunSnapshot({
			run_id: "r",
			status: "running",
			steps: [
				{ chat_id: "a", status: "reported" },
				{ chat_id: "b", status: "running" },
			],
		});
		expect(forkWorkflowStepProgress(snapshot?.steps ?? [])).toBe(
			"1 of 2 steps reported",
		);
	});
});

describe("forkWorkflowRunStatusMeta", () => {
	it("shows a status it does not know as a neutral badge", () => {
		expect(forkWorkflowRunStatusMeta("stopped")).toEqual({
			label: "Stopped",
			tone: "muted",
		});
	});
});
