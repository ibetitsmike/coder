import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { ForkInstallPluginPackDialog } from "./ForkInstallPluginPackDialog";

const meta = {
	title: "pages/AISettingsPage/ForkInstallPluginPackDialog",
	component: ForkInstallPluginPackDialog,
	args: {
		error: null,
		isInstalling: false,
		onInstall: fn(),
		onClose: fn(),
	},
} satisfies Meta<typeof ForkInstallPluginPackDialog>;

export default meta;
type Story = StoryObj<typeof ForkInstallPluginPackDialog>;

export const Default: Story = {};

export const Installing: Story = {
	args: { isInstalling: true },
};

export const InstallError: Story = {
	args: {
		error: mockApiError({
			message: "Host github.com is not an allowed plugin pack source.",
		}),
	},
};
