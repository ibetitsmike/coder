import type { Meta, StoryObj } from "@storybook/react-vite";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { ForkSettingsNavItems } from "./ForkSettingsNavItems";

const meta = {
	title: "pages/AgentsPage/ForkSettingsNavItems",
	component: ForkSettingsNavItems,
	args: { settingsSection: "general", state: null },
	decorators: [withDashboardProvider],
	parameters: { experiments: ["fork-agent-definitions"] },
} satisfies Meta<typeof ForkSettingsNavItems>;

export default meta;
type Story = StoryObj<typeof ForkSettingsNavItems>;

export const Inactive: Story = {};

export const Active: Story = {
	args: { settingsSection: "agent-definitions" },
};
