// This file is fork code: stories for the labeled long-text block
// (fork-workflows experiment). Pixel screenshots these stories to capture
// the collapsed and expanded visual states; the Vitest suite in
// ForkWorkflowLongText.test.tsx pins the control's state through role
// queries.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { ForkWorkflowLongText } from "./ForkWorkflowLongText";

const longText = Array.from(
	{ length: 13 },
	(_, index) => `line ${index}: ${"x".repeat(40)}`,
).join("\n");

const meta = {
	title: "pages/AgentsPage/components/ForkWorkflows/ForkWorkflowLongText",
	component: ForkWorkflowLongText,
	parameters: {
		layout: "centered",
	},
	decorators: [
		(Story) => (
			<div className="w-120">
				<Story />
			</div>
		),
	],
	args: {
		label: "Result",
		text: longText,
	},
} satisfies Meta<typeof ForkWorkflowLongText>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Collapsed: Story = {};

export const Expanded: Story = {
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", { name: "Show more" }),
		);
	},
};
