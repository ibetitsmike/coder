import { describe, expect, it } from "vitest";
import {
	MockChatWorkflowRun,
	MockChatWorkflowRunDetail,
	MockForkWorkflowCompletedResult,
	MockForkWorkflowNow,
	MockForkWorkflowRunId,
} from "#/testHelpers/forkWorkflows";
import {
	forkWorkflowRunDuration,
	forkWorkflowRunStatusMeta,
	forkWorkflowStepProgress,
	mergeForkWorkflowRunDetail,
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
		expect(forkWorkflowRunStatusMeta("queued")).toEqual({
			label: "Queued",
			tone: "muted",
		});
	});
});

describe("forkWorkflowRunDuration", () => {
	it("ends an interrupted run at its last update", () => {
		// Created 10:00:00, updated 10:03:20, now 10:05:00.
		const run = { ...MockChatWorkflowRun, finished_at: undefined };
		expect(
			forkWorkflowRunDuration(
				{ ...run, status: "interrupted" },
				MockForkWorkflowNow,
			),
		).toBe("3m 20s");
		expect(
			forkWorkflowRunDuration(
				{ ...run, status: "running" },
				MockForkWorkflowNow,
			),
		).toBe("5m");
	});
});

describe("mergeForkWorkflowRunDetail", () => {
	it("keeps the result's logs and reused steps that a running detail lacks", () => {
		const snapshot = parseForkWorkflowRunSnapshot({
			...MockForkWorkflowCompletedResult,
			status: "running",
			result: undefined,
			logs: ["checked 2 files"],
		});
		if (!snapshot) {
			throw new Error("the tool result did not parse");
		}
		const mockRunningDetail = {
			...MockChatWorkflowRunDetail,
			status: "running",
			logs: [],
			steps: MockChatWorkflowRunDetail.steps.map((step) => ({
				...step,
				reused: false,
			})),
		};

		const merged = mergeForkWorkflowRunDetail(snapshot, mockRunningDetail);
		expect(merged.status).toBe("running");
		expect(merged.logs).toEqual(["checked 2 files"]);
		expect(merged.steps.map((step) => step.reused)).toEqual([
			true,
			false,
			false,
		]);

		expect(
			mergeForkWorkflowRunDetail(snapshot, {
				...mockRunningDetail,
				status: "completed",
				logs: ["2 reviews"],
			}).logs,
		).toEqual(["2 reviews"]);
	});
});
