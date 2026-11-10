// This file is fork code: stories for the AI settings sidebar's
// fork-agent-definitions entry.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { useLocation } from "react-router";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	MockDefaultOrganization,
	MockNoPermissions,
	MockPermissions,
} from "#/testHelpers/entities";
import { withDashboardProvider } from "#/testHelpers/storybook";
import AISettingsSidebarView from "./AISettingsSidebarView";

const LocationProbe = () => {
	const location = useLocation();
	return (
		<p role="status" aria-label="Current location" className="sr-only">
			{location.pathname}
			{location.search}
		</p>
	);
};

const meta: Meta<typeof AISettingsSidebarView> = {
	title: "modules/management/AISettingsSidebarView/ForkAgentDefinitions",
	component: AISettingsSidebarView,
	decorators: [
		(Story) => (
			<>
				<Story />
				<LocationProbe />
			</>
		),
		withDashboardProvider,
	],
	args: {
		canViewOrganizationInstructions: false,
		skillOrganizations: [MockDefaultOrganization],
	},
	parameters: {
		experiments: ["fork-agent-definitions"],
		reactRouter: reactRouterParameters({
			location: {
				path: "/ai/settings/skills",
				searchParams: { org: MockDefaultOrganization.name },
			},
			routing: [
				{ path: "/ai/settings/skills", useStoryElement: true },
				{ path: "/ai/settings/agent-definitions", useStoryElement: true },
			],
		}),
	},
};

export default meta;
type Story = StoryObj<typeof AISettingsSidebarView>;

const followsAgentDefinitionsLink: Story["play"] = async ({
	canvasElement,
}) => {
	const canvas = within(canvasElement);
	await userEvent.click(
		canvas.getByRole("link", { name: "Agent definitions" }),
	);
	await waitFor(() =>
		expect(
			canvas.getByRole("status", { name: "Current location" }).textContent,
		).toBe(
			`/ai/settings/agent-definitions?org=${MockDefaultOrganization.name}`,
		),
	);
};

export const ForDeploymentAdmin: Story = {
	args: { permissions: MockPermissions },
	play: followsAgentDefinitionsLink,
};

export const ForOrganizationAuditor: Story = {
	args: {
		permissions: { ...MockNoPermissions, viewAnyOrganizationSkills: true },
	},
	play: followsAgentDefinitionsLink,
};
