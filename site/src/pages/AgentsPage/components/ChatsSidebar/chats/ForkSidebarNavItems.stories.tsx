import type { Meta, StoryObj } from "@storybook/react-vite";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { ForkSidebarNavItems } from "./ForkSidebarNavItems";

const meta = {
	title: "pages/AgentsPage/ForkSidebarNavItems",
	component: ForkSidebarNavItems,
	args: { state: null },
	decorators: [withDashboardProvider],
	parameters: { experiments: ["fork-agent-definitions"] },
} satisfies Meta<typeof ForkSidebarNavItems>;

export default meta;
type Story = StoryObj<typeof ForkSidebarNavItems>;

export const Default: Story = {};
