// This file is fork code: tests for the step list of a Starlark workflow run.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { MockChatWorkflowRunDetail } from "#/testHelpers/forkWorkflows";
import { render } from "#/testHelpers/renderHelpers";
import { ForkWorkflowSteps } from "./ForkWorkflowSteps";
import type { ForkWorkflowStep } from "./forkWorkflows";

type StepInput = {
	chatId: string;
	title: string;
	status: string;
	reused?: boolean;
	error?: string;
	phase: string;
};

const renderSteps = (stepInputs: readonly StepInput[]) => {
	const run: WorkflowRunDetail = {
		...MockChatWorkflowRunDetail,
		phases: ["plan", "build"],
		current_phase: "build",
		steps: stepInputs.map((step, ordinal) => ({
			id: step.chatId,
			run_id: MockChatWorkflowRunDetail.id,
			title: step.title,
			status: step.status,
			reused: step.reused === true,
			error: step.error ?? "",
			ordinal,
			phase: step.phase,
			started_at: "2026-11-12T10:00:05Z",
			finished_at: "2026-11-12T10:03:00Z",
		})),
	};
	const mappedSteps: readonly ForkWorkflowStep[] = run.steps.map((step) => ({
		chatId: step.id,
		title: step.title,
		status: step.status,
		reused: step.reused === true,
		error: step.error ?? "",
		phase: step.phase,
	}));
	render(
		<ForkWorkflowSteps
			steps={mappedSteps}
			phases={run.phases}
			currentPhase={run.current_phase}
			runStatus={run.status}
		/>,
	);
	return run;
};

describe("ForkWorkflowSteps", () => {
	it("renders each phase heading once, before the steps of that phase", () => {
		renderSteps([
			{
				chatId: "aa111111-1111-4111-8111-111111111111",
				title: "Plan the change",
				status: "reported",
				phase: "plan",
			},
			{
				chatId: "bb222222-2222-4222-8222-222222222222",
				title: "Build the change",
				status: "running",
				phase: "build",
			},
		]);

		expect(screen.getByRole("list", { name: "Workflow steps" })).toBeDefined();
		expect(screen.getAllByTitle("plan")).toHaveLength(2);
		expect(screen.getAllByTitle("build")).toHaveLength(2);
		expect(screen.getByText("Plan the change")).toBeDefined();
		expect(screen.getByText("Build the change")).toBeDefined();
	});

	it("renders a heading again when the script re-enters a phase", () => {
		renderSteps([
			{
				chatId: "aa111111-1111-4111-8111-111111111111",
				title: "Plan the change",
				status: "reported",
				phase: "plan",
			},
			{
				chatId: "bb222222-2222-4222-8222-222222222222",
				title: "Build the change",
				status: "reported",
				phase: "build",
			},
			{
				chatId: "cc333333-3333-4333-8333-333333333333",
				title: "Plan the followup",
				status: "running",
				phase: "plan",
			},
		]);

		// The re-entered phase gets its own heading, so the later plan step
		// does not sit under the build heading. Three plan elements now: the
		// rail pill plus one heading per phase group.
		expect(screen.getAllByTitle("plan")).toHaveLength(3);
		expect(screen.getAllByTitle("build")).toHaveLength(2);
		expect(screen.getByText("Plan the followup")).toBeDefined();
	});

	it("renders steps that run before the first phase() call without a heading", () => {
		renderSteps([
			{
				chatId: "aa111111-1111-4111-8111-111111111111",
				title: "Pick a file",
				status: "reported",
				phase: "",
			},
			{
				chatId: "bb222222-2222-4222-8222-222222222222",
				title: "Build the change",
				status: "running",
				phase: "build",
			},
		]);

		expect(screen.getAllByTitle("build")).toHaveLength(2);
		expect(screen.getByText("Pick a file")).toBeDefined();
		expect(screen.getByText("Build the change")).toBeDefined();
	});
});
