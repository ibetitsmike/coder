import { describe, expect, it } from "vitest";
import {
	MockChatWorkflowRun,
	MockChatWorkflowRunDetail,
	MockForkWorkflowCompletedResult,
	MockForkWorkflowNow,
	MockForkWorkflowRunId,
} from "#/testHelpers/forkWorkflows";
import {
	forkWorkflowPhaseState,
	forkWorkflowPhaseStateOf,
	forkWorkflowRunDuration,
	forkWorkflowRunStatusMeta,
	forkWorkflowStepPhases,
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
			phases: ["review", "summarize"],
			currentPhase: "summarize",
			steps: MockForkWorkflowCompletedResult.steps.map((step) => ({
				chatId: step.chat_id,
				title: step.title,
				status: step.status,
				reused: step.reused === true,
				error: "",
				phase: step.phase,
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

describe("phases", () => {
	it("defaults to no phases when the tool result omits them", () => {
		const snapshot = parseForkWorkflowRunSnapshot({
			run_id: "r",
			status: "running",
			steps: [{ chat_id: "a", status: "running" }],
		});
		expect(snapshot?.phases).toEqual([]);
		expect(snapshot?.currentPhase).toBe("");
		expect(snapshot?.steps[0]?.phase).toBe("");
	});

	it("drops non-string and empty names from a phase list", () => {
		const snapshot = parseForkWorkflowRunSnapshot({
			run_id: "r",
			status: "running",
			phases: ["review", "", 42, "summarize"],
			steps: [],
		});
		expect(snapshot?.phases).toEqual(["review", "summarize"]);
	});

	it("groups steps by phase in step order", () => {
		const snapshot = parseForkWorkflowRunSnapshot({
			run_id: "r",
			status: "running",
			phases: ["review", "summarize", "publish"],
			steps: [
				{ chat_id: "a", status: "reported" },
				{ chat_id: "b", status: "running", phase: "review" },
				{ chat_id: "c", status: "reported", phase: "summarize" },
			],
		});
		const steps = snapshot?.steps ?? [];
		expect(forkWorkflowStepPhases(steps).map((group) => group.phase)).toEqual([
			"",
			"review",
			"summarize",
		]);
		expect(forkWorkflowPhaseState(steps.filter((s) => s.phase === ""))).toBe(
			"done",
		);
		expect(
			forkWorkflowPhaseState(steps.filter((s) => s.phase === "review")),
		).toBe("running");
		expect(
			forkWorkflowPhaseState(steps.filter((s) => s.phase === "summarize")),
		).toBe("done");
		expect(forkWorkflowPhaseState([])).toBe("pending");
	});
});

describe("forkWorkflowPhaseStateOf", () => {
	const phases = ["plan", "build", "review"];
	const step = (chatId: string, phase: string, status: string) => ({
		chatId,
		title: chatId,
		status,
		reused: false,
		error: "",
		phase,
	});

	it("keeps a not-yet-reached phase pending on a live run", () => {
		expect(
			forkWorkflowPhaseStateOf("review", 2, phases, "plan", [], "running"),
		).toBe("pending");
	});

	it("shows a reached phase with no steps as running while the run is live", () => {
		expect(
			forkWorkflowPhaseStateOf("build", 1, phases, "build", [], "running"),
		).toBe("running");
	});

	it("shows a passed phase with no steps as done", () => {
		expect(
			forkWorkflowPhaseStateOf("plan", 0, phases, "build", [], "running"),
		).toBe("done");
	});

	it("derives the state from the steps", () => {
		const reported = [step("a", "build", "reported")];
		expect(
			forkWorkflowPhaseStateOf(
				"build",
				1,
				phases,
				"build",
				reported,
				"running",
			),
		).toBe("done");
		const running = [step("a", "build", "running")];
		expect(
			forkWorkflowPhaseStateOf("build", 1, phases, "build", running, "running"),
		).toBe("running");
	});

	it("marks unfinished phases failed on a failed run", () => {
		// The last phase never started a step: it failed with the run.
		expect(
			forkWorkflowPhaseStateOf("review", 2, phases, "review", [], "failed"),
		).toBe("failed");
		// A phase the script never reached stays pending.
		expect(
			forkWorkflowPhaseStateOf("review", 2, phases, "plan", [], "failed"),
		).toBe("pending");
	});

	it("marks a passed phase with no steps done even on a failed run", () => {
		expect(
			forkWorkflowPhaseStateOf("plan", 0, phases, "build", [], "failed"),
		).toBe("done");
	});

	it("keeps unreached phases pending on a failed run", () => {
		expect(
			forkWorkflowPhaseStateOf("review", 2, phases, "plan", [], "stopped"),
		).toBe("pending");
	});

	it("marks only the current phase failed on a stopped run", () => {
		expect(
			forkWorkflowPhaseStateOf("review", 2, phases, "review", [], "stopped"),
		).toBe("failed");
		const reported = [step("a", "review", "reported")];
		expect(
			forkWorkflowPhaseStateOf(
				"review",
				2,
				phases,
				"review",
				reported,
				"stopped",
			),
		).toBe("done");
	});

	it("marks every reached phase done on a completed run", () => {
		// On a completed run the script reached the last phase, so the run's
		// rail shows plan and build as done even when they spawned no steps.
		expect(
			forkWorkflowPhaseStateOf("plan", 0, phases, "review", [], "completed"),
		).toBe("done");
		expect(
			forkWorkflowPhaseStateOf("build", 1, phases, "review", [], "completed"),
		).toBe("done");
		expect(
			forkWorkflowPhaseStateOf("review", 2, phases, "review", [], "completed"),
		).toBe("done");
	});

	it("keeps an interrupted run's phases waiting", () => {
		expect(
			forkWorkflowPhaseStateOf("build", 1, phases, "build", [], "interrupted"),
		).toBe("running");
	});

	it("shows a passed phase with a failed step as done", () => {
		// The run failed or stopped in a later phase and cleanup marked the
		// phase's open step failed. The script had moved past the phase, so
		// only the phase the run ended in shows failed.
		const failed = [step("a", "build", "failed")];
		expect(
			forkWorkflowPhaseStateOf("build", 1, phases, "review", failed, "failed"),
		).toBe("done");
		expect(
			forkWorkflowPhaseStateOf("review", 2, phases, "review", failed, "failed"),
		).toBe("failed");
	});

	it("keeps a passed phase with unfinished steps running on a live run", () => {
		// A spawn() step can outlive the phase that started it, so a passed
		// phase on a live run keeps moving until its steps settle.
		const running = [step("a", "build", "running")];
		expect(
			forkWorkflowPhaseStateOf(
				"build",
				1,
				phases,
				"review",
				running,
				"running",
			),
		).toBe("running");
		const reminded = [step("a", "plan", "reminded")];
		expect(
			forkWorkflowPhaseStateOf(
				"plan",
				0,
				phases,
				"review",
				reminded,
				"running",
			),
		).toBe("running");
		// Once the step reports, the passed phase is done again.
		const reported = [step("a", "build", "reported")];
		expect(
			forkWorkflowPhaseStateOf(
				"build",
				1,
				phases,
				"review",
				reported,
				"running",
			),
		).toBe("done");
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

	it("keeps the snapshot's newer phase state while the polled detail lags", () => {
		// The snapshot saw a phase() the detail's best-effort journal write
		// missed, so the merge keeps the snapshot's phase list instead of
		// moving the rail backward.
		const snapshot = parseForkWorkflowRunSnapshot({
			run_id: "r",
			status: "running",
			phases: ["review", "summarize", "publish"],
			current_phase: "publish",
			steps: [],
		});
		if (!snapshot) {
			throw new Error("the tool result did not parse");
		}
		const merged = mergeForkWorkflowRunDetail(snapshot, {
			...MockChatWorkflowRunDetail,
			status: "running",
			logs: [],
			phases: ["review"],
			current_phase: "review",
		});
		expect(merged.phases).toEqual(["review", "summarize", "publish"]);
		expect(merged.currentPhase).toBe("publish");

		// A detail that ends the run is the authority again.
		const ended = mergeForkWorkflowRunDetail(snapshot, {
			...MockChatWorkflowRunDetail,
			status: "completed",
			logs: ["2 reviews"],
			phases: ["review"],
			current_phase: "review",
		});
		expect(ended.phases).toEqual(["review"]);
		expect(ended.currentPhase).toBe("review");
	});

	it("takes the phase state of a detail ahead of the snapshot", () => {
		// The tool result froze at the run_workflow or await_workflow call,
		// so a detail that has moved past it must win, or the rail stops
		// moving until the run ends.
		const snapshot = parseForkWorkflowRunSnapshot({
			run_id: "r",
			status: "running",
			phases: ["plan", "build", "review"],
			current_phase: "plan",
			steps: [],
		});
		if (!snapshot) {
			throw new Error("the tool result did not parse");
		}
		const ahead = mergeForkWorkflowRunDetail(snapshot, {
			...MockChatWorkflowRunDetail,
			status: "running",
			logs: [],
			phases: ["plan", "build", "review", "extra"],
			current_phase: "extra",
		});
		expect(ahead.phases).toEqual(["plan", "build", "review", "extra"]);
		expect(ahead.currentPhase).toBe("extra");

		// The snapshot had no current phase yet and the detail moved on.
		const resumed = parseForkWorkflowRunSnapshot({
			run_id: "r",
			status: "running",
			phases: ["plan", "build"],
			current_phase: "",
			steps: [],
		});
		if (!resumed) {
			throw new Error("the tool result did not parse");
		}
		const moved = mergeForkWorkflowRunDetail(resumed, {
			...MockChatWorkflowRunDetail,
			status: "running",
			logs: [],
			phases: ["plan", "build", "extra"],
			current_phase: "extra",
		});
		expect(moved.phases).toEqual(["plan", "build", "extra"]);
		expect(moved.currentPhase).toBe("extra");
	});
});
