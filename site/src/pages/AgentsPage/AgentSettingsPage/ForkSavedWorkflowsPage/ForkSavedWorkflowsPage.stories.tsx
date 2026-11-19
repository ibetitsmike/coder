import type { Meta, StoryObj } from "@storybook/react-vite";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { savedWorkflowsKey } from "#/api/queries/forkSavedWorkflows";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockUserMember,
} from "#/testHelpers/entities";
import {
	MockForkOrganizationSavedWorkflowMetadata,
	MockForkSavedWorkflowMetadata,
} from "#/testHelpers/forkSavedWorkflows";
import {
	withAuthProvider,
	withDashboardProvider,
	withToaster,
} from "#/testHelpers/storybook";
import ForkSavedWorkflowsPage from "./ForkSavedWorkflowsPage";

const path = "/agents/settings/workflows";

const meta = {
	title: "pages/AgentsPage/ForkSavedWorkflowsPage",
	component: ForkSavedWorkflowsPage,
	decorators: [withToaster, withAuthProvider, withDashboardProvider],
	parameters: {
		user: {
			...MockUserMember,
			organization_ids: [MockDefaultOrganization.id],
		},
		experiments: ["fork-workflows"],
		organizations: [MockDefaultOrganization],
		reactRouter: reactRouterParameters({
			location: { path },
			routing: { path },
		}),
	},
} satisfies Meta<typeof ForkSavedWorkflowsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Listed: Story = {
	parameters: {
		queries: [
			{
				key: savedWorkflowsKey({
					tier: "user",
					organizationId: MockDefaultOrganization.id,
				}),
				data: [MockForkSavedWorkflowMetadata],
			},
			{
				key: savedWorkflowsKey({
					tier: "organization",
					organizationId: MockDefaultOrganization.id,
				}),
				data: [MockForkOrganizationSavedWorkflowMetadata],
			},
		],
	},
};

export const Empty: Story = {
	parameters: {
		queries: [
			{
				key: savedWorkflowsKey({
					tier: "user",
					organizationId: MockDefaultOrganization.id,
				}),
				data: [],
			},
			{
				key: savedWorkflowsKey({
					tier: "organization",
					organizationId: MockDefaultOrganization.id,
				}),
				data: [],
			},
		],
	},
};

export const MultipleOrganizations: Story = {
	parameters: {
		user: {
			...MockUserMember,
			organization_ids: [MockDefaultOrganization.id, MockOrganization2.id],
		},
		organizations: [MockDefaultOrganization, MockOrganization2],
		queries: [
			{
				key: savedWorkflowsKey({
					tier: "user",
					organizationId: MockDefaultOrganization.id,
				}),
				data: [MockForkSavedWorkflowMetadata],
			},
			{
				key: savedWorkflowsKey({
					tier: "organization",
					organizationId: MockDefaultOrganization.id,
				}),
				data: [MockForkOrganizationSavedWorkflowMetadata],
			},
		],
	},
};

export const NoOrganization: Story = {
	parameters: {
		user: { ...MockUserMember, organization_ids: [] },
	},
};
