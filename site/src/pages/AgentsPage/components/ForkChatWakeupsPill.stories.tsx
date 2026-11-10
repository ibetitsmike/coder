import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, userEvent, within } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockChatWakeupCompleted,
	MockChatWakeupCron,
	MockChatWakeupEvents,
	MockChatWakeupInterval,
	MockChatWakeupPaused,
} from "#/testHelpers/forkChatWakeups";
import { ForkChatWakeupsPillView } from "./ForkChatWakeupsPill";

const meta: Meta<typeof ForkChatWakeupsPillView> = {
	title: "pages/AgentsPage/ForkChatWakeupsPill",
	component: ForkChatWakeupsPillView,
	args: {
		wakeups: [
			MockChatWakeupInterval,
			MockChatWakeupCron,
			MockChatWakeupEvents,
			MockChatWakeupPaused,
			MockChatWakeupCompleted,
		],
		error: undefined,
		onUpdate: fn(),
	},
};

export default meta;
type Story = StoryObj<typeof ForkChatWakeupsPillView>;

export const Collapsed: Story = {};

const expand = async (canvasElement: HTMLElement) => {
	await userEvent.click(
		within(canvasElement).getByRole("button", { name: /Wake-ups/ }),
	);
};

export const Expanded: Story = {
	play: async ({ canvasElement }) => {
		await expand(canvasElement);
	},
};

export const ExpandedMobile: Story = {
	parameters: {
		viewport: { defaultViewport: "mobile1" },
		pixel: { matrix: { viewports: ["phone"] } },
	},
	play: Expanded.play,
};

export const LoadError: Story = {
	args: {
		wakeups: undefined,
		error: mockApiError({ message: "You do not have access to this chat." }),
	},
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", {
				name: "Wake-ups failed to load",
			}),
		);
	},
};

export const UpdateError: Story = {
	args: {
		updateError: {
			id: MockChatWakeupInterval.id,
			message: "This wake-up has already finished.",
		},
	},
	play: Expanded.play,
};
