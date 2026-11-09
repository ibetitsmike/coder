import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { linkablePluginsKey } from "#/api/queries/plugins";
import { MockLinkablePlugin, MockPluginUserLink } from "#/testHelpers/plugins";
import LinkedAccountsPage from "./LinkedAccountsPage";

const meta: Meta<typeof LinkedAccountsPage> = {
	title: "pages/UserSettingsPage/LinkedAccountsPage",
	component: LinkedAccountsPage,
};

export default meta;
type Story = StoryObj<typeof LinkedAccountsPage>;

export const UnlinkConfirmation: Story = {
	parameters: {
		queries: [{ key: linkablePluginsKey, data: [MockLinkablePlugin] }],
	},
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", {
				name: `Unlink ${MockPluginUserLink.external_id}`,
			}),
		);
	},
};
