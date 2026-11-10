import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, userEvent, within } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockChatWakeupCron,
	MockChatWakeupCompleted,
	MockChatWakeupEvents,
	MockChatWakeupInterval,
	MockChatWakeupPaused,
} from "#/testHelpers/forkChatWakeups";
import { ForkChatWakeupsPillView } from "./ForkChatWakeupsPill";

const meta: Meta<typeof ForkChatWakeupsPillView> = {
	title: "pages/AgentsPage/ForkChatWakeupsPill",
	component: ForkChatWakeupsPillView,
	args: {
		wakeups: [MockChatWakeupInterval],
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

export const Cron: Story = {
	args: {
		wakeups: [MockChatWakeupCron],
	},
	play: Expanded.play,
};

export const Events: Story = {
	args: {
		wakeups: [MockChatWakeupEvents],
	},
	play: Expanded.play,
};

export const Paused: Story = {
	args: {
		wakeups: [MockChatWakeupPaused],
	},
	play: Expanded.play,
};

export const LongContent: Story = {
	args: {
		wakeups: [
			{
				...MockChatWakeupInterval,
				prompt:
					"Trace every caller of the transcript reader, confirm each one passes the owner's credentials, and note the callers that read a descendant chat through the parent walk",
			},
		],
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
		wakeups: [MockChatWakeupInterval, MockChatWakeupCompleted],
		updateError: {
			id: MockChatWakeupInterval.id,
			message: "This wake-up has already finished.",
		},
	},
	play: Expanded.play,
};