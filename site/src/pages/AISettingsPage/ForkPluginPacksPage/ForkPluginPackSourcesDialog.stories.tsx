import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, userEvent, within } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { ForkPluginPackSourcesDialog } from "./ForkPluginPackSourcesDialog";

const meta = {
	title: "pages/AISettingsPage/ForkPluginPackSourcesDialog",
	component: ForkPluginPackSourcesDialog,
	args: {
		hosts: ["github.com", "git.example.com"],
		loadError: undefined,
		saveError: undefined,
		isSaving: false,
		onSave: fn(),
		onClose: fn(),
	},
} satisfies Meta<typeof ForkPluginPackSourcesDialog>;

export default meta;
type Story = StoryObj<typeof ForkPluginPackSourcesDialog>;

export const Default: Story = {};

export const Empty: Story = {
	args: { hosts: [] },
};

export const Loading: Story = {
	args: { hosts: undefined },
};

export const LoadError: Story = {
	args: {
		hosts: undefined,
		loadError: mockApiError({ message: "Failed to load the sources." }),
	},
};

export const InvalidHost: Story = {
	play: async () => {
		const dialog = within(await screen.findByRole("dialog"));
		const field = dialog.getByLabelText(/^Source hosts/);
		await userEvent.type(field, "\nhttps://gitlab.com");
		await userEvent.tab();
	},
};

export const SaveError: Story = {
	args: {
		saveError: mockApiError({
			message: "Invalid plugin pack source host.",
			detail:
				'"git.example.com:22" must be a host name without a scheme, port or path.',
		}),
	},
};

export const Saving: Story = {
	args: { isSaving: true },
};
