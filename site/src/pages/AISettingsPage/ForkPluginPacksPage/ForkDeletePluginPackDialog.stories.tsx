import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { MockForkPluginPack } from "#/testHelpers/forkPluginPacks";
import { ForkDeletePluginPackDialog } from "./ForkDeletePluginPackDialog";

const meta = {
	title: "pages/AISettingsPage/ForkDeletePluginPackDialog",
	component: ForkDeletePluginPackDialog,
	args: {
		pack: MockForkPluginPack,
		error: null,
		isDeleting: false,
		onClose: fn(),
		onConfirm: fn(),
	},
} satisfies Meta<typeof ForkDeletePluginPackDialog>;

export default meta;
type Story = StoryObj<typeof ForkDeletePluginPackDialog>;

export const Default: Story = {};

export const Deleting: Story = {
	args: { isDeleting: true },
};

export const DeleteError: Story = {
	args: {
		error: mockApiError({ message: "Failed to delete plugin pack." }),
	},
};
