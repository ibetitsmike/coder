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
	// The visual groupings (one heading per phase, a re-entered phase
	// rendering its heading again) are captured in ForkWorkflowSteps.stories.tsx.
	it("describes the first step of each phase group with that phase heading", () => {
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

		// Every step of a phase group is described by that group's heading,
		// so a screen reader announces the phase for each step it lists.
		expect(screen.getAllByTitle("plan")).toHaveLength(2);
		expect(screen.getAllByTitle("build")).toHaveLength(2);
		expect(screen.getByText("Plan the change")).toBeDefined();
		expect(screen.getByText("Build the change")).toBeDefined();
		const planHeadingId = screen
			.getAllByTitle("plan")
			.map((el) => el.getAttribute("id"))
			.find((id) => id !== null && id !== "");
		expect(planHeadingId).toBeDefined();
		const buildHeadingId = screen
			.getAllByTitle("build")
			.map((el) => el.getAttribute("id"))
			.find((id) => id !== null && id !== "");
		expect(buildHeadingId).toBeDefined();
		expect(screen.getByText("Plan the change").closest("li")).toHaveAttribute(
			"aria-describedby",
			planHeadingId as string,
		);
		expect(screen.getByText("Build the change").closest("li")).toHaveAttribute(
			"aria-describedby",
			buildHeadingId as string,
		);
	});

	it("does not describe a step whose phase group has no heading", () => {
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
		// does not read as part of the build group. Three plan elements now: the
		// rail pill plus one heading per phase group.
		expect(screen.getAllByTitle("plan")).toHaveLength(3);
		expect(screen.getAllByTitle("build")).toHaveLength(2);
		expect(screen.getByText("Plan the followup")).toBeDefined();
		const planHeadingIds = screen
			.getAllByTitle("plan")
			.map((el) => el.getAttribute("id"))
			.filter((id) => id !== null && id !== "");
		expect(planHeadingIds).toHaveLength(2);
		const describedBy = screen
			.getAllByRole("listitem")
			.filter((el) => el.hasAttribute("aria-describedby"))
			.map((el) => el.getAttribute("aria-describedby"));
		expect(describedBy).toHaveLength(3);
		expect(new Set(describedBy).size).toBe(3);
		expect(planHeadingIds).toContain(describedBy[2]);
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

		// The pre-phase step is not described by any heading, and the build
		// step is described by the build heading.
		expect(screen.getAllByTitle("build")).toHaveLength(2);
		expect(screen.getByText("Pick a file")).toBeDefined();
		expect(screen.getByText("Build the change")).toBeDefined();
		const pickLi = screen.getByText("Pick a file").closest("li");
		expect(pickLi).not.toHaveAttribute("aria-describedby");
		const buildHeadingId = screen
			.getAllByTitle("build")
			.map((el) => el.getAttribute("id"))
			.find((id) => id !== null && id !== "");
		expect(buildHeadingId).toBeDefined();
		expect(screen.getByText("Build the change").closest("li")).toHaveAttribute(
			"aria-describedby",
			buildHeadingId as string,
		);
	});
});
