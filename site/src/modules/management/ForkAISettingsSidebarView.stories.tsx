// This file is fork code: stories for the AI settings sidebar's
// fork-agent-definitions entry.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	MockDefaultOrganization,
	MockNoPermissions,
	MockPermissions,
} from "#/testHelpers/entities";
import { withDashboardProvider } from "#/testHelpers/storybook";
import AISettingsSidebarView from "./AISettingsSidebarView";

const meta: Meta<typeof AISettingsSidebarView> = {
	title: "modules/management/AISettingsSidebarView/ForkAgentDefinitions",
	component: AISettingsSidebarView,
	decorators: [withDashboardProvider],
	args: {
		canViewOrganizationInstructions: false,
		skillOrganizations: [MockDefaultOrganization],
	},
	parameters: {
		experiments: ["fork-agent-definitions"],
		reactRouter: reactRouterParameters({
			location: { path: "/ai/settings/agent-definitions" },
			routing: { path: "/ai/settings/agent-definitions" },
		}),
	},
};

export default meta;
type Story = StoryObj<typeof AISettingsSidebarView>;

export const ForDeploymentAdmin: Story = {
	args: { permissions: MockPermissions },
};

export const ForOrganizationAuditor: Story = {
	args: {
		permissions: { ...MockNoPermissions, viewAnyOrganizationSkills: true },
	},
};
