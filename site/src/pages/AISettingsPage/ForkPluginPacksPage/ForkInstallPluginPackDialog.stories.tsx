import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { fn, screen, userEvent, within } from "storybook/test";
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

export const ValidationError: Story = {
	play: async () => {
		const dialog = within(await screen.findByRole("dialog"));
		await userEvent.click(dialog.getByLabelText(/^Ref/));
		await userEvent.tab();
	},
};

const submitValidForm = async () => {
	const dialog = within(await screen.findByRole("dialog"));
	await userEvent.type(dialog.getByLabelText(/^Name/), "pstack");
	await userEvent.type(
		dialog.getByLabelText(/^Git repository URL/),
		"https://git.example.com/cursor/plugins.git",
	);
	await userEvent.type(dialog.getByLabelText(/^Path/), "pstack");
	await userEvent.type(dialog.getByLabelText(/^Ref/), "v0.15.36");
	await userEvent.click(dialog.getByRole("button", { name: "Install" }));
};

type SubmittedDialogProps = { error?: unknown };

const SubmittedDialog: React.FC<SubmittedDialogProps> = ({ error }) => {
	const [isSubmitted, setIsSubmitted] = useState(false);
	return (
		<ForkInstallPluginPackDialog
			error={isSubmitted ? error : null}
			isInstalling={isSubmitted && !error}
			onInstall={() => setIsSubmitted(true)}
			onClose={() => {}}
		/>
	);
};

export const Installing: Story = {
	render: () => <SubmittedDialog />,
	play: submitValidForm,
};

export const InstallError: Story = {
	render: () => (
		<SubmittedDialog
			error={mockApiError({
				message: "Host git.example.com is not an allowed plugin pack source.",
			})}
		/>
	),
	play: submitValidForm,
};
