import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { fn, userEvent, within } from "storybook/test";
import type { ChatWakeup } from "#/api/typesGenerated";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockChatWakeupCompleted,
	MockChatWakeupCron,
	MockChatWakeupDelay,
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
			MockChatWakeupDelay,
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

export const Expanded: Story = {
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", { name: /Wake-ups/ }),
		);
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

export const RefreshErrorWithNoOpenWakeups: Story = {
	args: {
		wakeups: [MockChatWakeupCompleted],
		error: mockApiError({ message: "The server is unavailable." }),
	},
	play: LoadError.play,
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

export const ActionsDisabled: Story = {
	args: { isDisabled: true },
	play: Expanded.play,
};

export const RefreshError: Story = {
	args: {
		error: mockApiError({ message: "Could not reach the server." }),
	},
	play: Expanded.play,
};

export const CanceledLast: Story = {
	args: { wakeups: [MockChatWakeupInterval] },
	render: function Render(args) {
		const [wakeups, setWakeups] = useState<readonly ChatWakeup[] | undefined>(
			args.wakeups,
		);
		return (
			<ForkChatWakeupsPillView
				{...args}
				wakeups={wakeups}
				onUpdate={(wakeup, status) =>
					setWakeups((old) =>
						old?.map((w) => (w.id === wakeup.id ? { ...w, status } : w)),
					)
				}
			/>
		);
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole("button", { name: /Wake-ups/ }));
		const body = within(canvasElement.ownerDocument.body);
		await userEvent.click(body.getByRole("button", { name: /^Cancel / }));
	},
};
