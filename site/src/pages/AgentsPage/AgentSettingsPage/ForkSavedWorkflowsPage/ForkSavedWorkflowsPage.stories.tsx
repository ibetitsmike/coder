import type { Meta, StoryObj } from "@storybook/react-vite";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { savedWorkflowsKey } from "#/api/queries/forkSavedWorkflows";
import type { ForkSavedWorkflowMetadata } from "#/api/typesGenerated";
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

const workflowQueries = (
	organizationId: string,
	personal: readonly ForkSavedWorkflowMetadata[],
	organization: readonly ForkSavedWorkflowMetadata[],
) => [
	{
		key: savedWorkflowsKey({ tier: "user", organizationId }),
		data: personal,
	},
	{
		key: savedWorkflowsKey({ tier: "organization", organizationId }),
		data: organization,
	},
];

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
		queries: workflowQueries(
			MockDefaultOrganization.id,
			[MockForkSavedWorkflowMetadata],
			[MockForkOrganizationSavedWorkflowMetadata],
		),
	},
};

export const Empty: Story = {
	parameters: {
		queries: workflowQueries(MockDefaultOrganization.id, [], []),
	},
};

export const MultipleOrganizations: Story = {
	parameters: {
		user: {
			...MockUserMember,
			organization_ids: [MockDefaultOrganization.id, MockOrganization2.id],
		},
		organizations: [MockDefaultOrganization, MockOrganization2],
		queries: workflowQueries(
			MockDefaultOrganization.id,
			[MockForkSavedWorkflowMetadata],
			[MockForkOrganizationSavedWorkflowMetadata],
		),
	},
};

export const NoOrganization: Story = {
	parameters: {
		user: { ...MockUserMember, organization_ids: [] },
	},
};
