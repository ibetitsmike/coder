// This file is fork code: tests for the step list of a Starlark workflow run.
// The visual groupings (one heading per phase, a re-entered phase rendering
// its heading again) are captured in ForkWorkflowSteps.stories.tsx; these
// tests pin the aria-described-by wiring those headings provide.

import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import {
	MockChatWorkflowRunDetail,
	MockForkWorkflowStepValue,
} from "#/testHelpers/forkWorkflows";
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
			value: step.status === "reported" ? MockForkWorkflowStepValue : undefined,
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
		value: run.steps.find((runStep) => runStep.id === step.id)?.value,
	}));
	render(
		<ForkWorkflowSteps
			steps={mappedSteps}
			phases={run.phases}
			currentPhase={run.current_phase}
			runStatus={run.status}
		/>,
	);
	const stepList = within(screen.getByRole("list", { name: "Workflow steps" }));
	return { run, stepList };
};

describe("ForkWorkflowSteps", () => {
	it("describes the first step of each phase group with that phase heading", () => {
		const { stepList } = renderSteps([
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

		const [planStep, buildStep] = stepList.getAllByRole("listitem");
		expect(planStep).toHaveAccessibleDescription("plan");
		expect(buildStep).toHaveAccessibleDescription("build");
	});

	it("describes only the first step of a phase group with that heading", () => {
		const { stepList } = renderSteps([
			{
				chatId: "aa111111-1111-4111-8111-111111111111",
				title: "Plan the change",
				status: "reported",
				phase: "plan",
			},
			{
				chatId: "bb222222-2222-4222-8222-222222222222",
				title: "Plan the review",
				status: "running",
				phase: "plan",
			},
		]);

		const [firstPlanStep, secondPlanStep] = stepList.getAllByRole("listitem");
		expect(firstPlanStep).toHaveAccessibleDescription("plan");
		expect(secondPlanStep).not.toHaveAccessibleDescription("plan");
	});

	it("describes the re-entered phase group with its own heading", () => {
		const { stepList } = renderSteps([
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

		const [planStep, buildStep, reenteredPlanStep] =
			stepList.getAllByRole("listitem");
		expect(planStep).toHaveAccessibleDescription("plan");
		expect(buildStep).toHaveAccessibleDescription("build");
		expect(reenteredPlanStep).toHaveAccessibleDescription("plan");
	});

	it("does not describe a step that runs before the first phase() call", () => {
		const { stepList } = renderSteps([
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

		const [prePhaseStep, buildStep] = stepList.getAllByRole("listitem");
		expect(prePhaseStep).toHaveAccessibleDescription("");
		expect(buildStep).toHaveAccessibleDescription("build");
	});

	it("names each reported value by the step's position in the list", () => {
		renderSteps([
			{
				chatId: "aa111111-1111-4111-8111-111111111111",
				title: "Review the file",
				status: "reported",
				phase: "plan",
			},
			{
				chatId: "bb222222-2222-4222-8222-222222222222",
				title: "Review the file",
				status: "reported",
				phase: "build",
			},
		]);

		// Two steps can share a title, so the region names carry the
		// position each value sits at, and they are never identical.
		const values = screen.getAllByRole("region", { name: /value$/ });
		expect(values[0]).toHaveAccessibleName("Step 1: Review the file value");
		expect(values[1]).toHaveAccessibleName("Step 2: Review the file value");
	});
});
