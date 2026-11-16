// This file is fork code: stories for the step list of a Starlark workflow
// run (fork-workflows experiment). Pixel screenshots these stories to capture
// the visual phase groupings; the Vitest suite in ForkWorkflowSteps.test.tsx
// pins the aria-described-by wiring those headings provide.

import type { Meta, StoryObj } from "@storybook/react-vite";
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

const toSteps = (stepInputs: readonly StepInput[]): ForkWorkflowStep[] =>
	stepInputs.map((step) => ({
		chatId: step.chatId,
		title: step.title,
		status: step.status,
		reused: step.reused === true,
		error: step.error ?? "",
		phase: step.phase,
	}));

const meta = {
	title: "pages/AgentsPage/components/ForkWorkflows/ForkWorkflowSteps",
	component: ForkWorkflowSteps,
	parameters: {
		layout: "centered",
	},
	decorators: [
		(Story) => (
			<div style={{ width: 480 }}>
				<Story />
			</div>
		),
	],
	args: {
		phases: ["plan", "build"],
		currentPhase: "build",
		runStatus: "running",
		steps: [],
	},
} satisfies Meta<typeof ForkWorkflowSteps>;

export default meta;
type Story = StoryObj<typeof meta>;

export const PhasesInRunOrder: Story = {
	args: {
		steps: toSteps([
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
		]),
	},
};

// A script can call phase() twice for the same phase. Each run of the
// phase gets its own heading, so the later steps do not read as part of
// the build group.
export const ReenteredPhase: Story = {
	args: {
		steps: toSteps([
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
		]),
	},
};

// Steps that run before the first phase() call follow no heading.
export const StepsBeforeFirstPhase: Story = {
	args: {
		steps: toSteps([
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
		]),
	},
};
