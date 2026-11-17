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
			within(canvasElement).getByRole("button", {
				name: "Show more of Result",
			}),
		);
	},
};
