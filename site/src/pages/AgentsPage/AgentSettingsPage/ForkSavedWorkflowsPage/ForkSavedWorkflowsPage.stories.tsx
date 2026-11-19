import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
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
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await canvas.findByRole("table", { name: "Organization workflows" });
		// The member manages personal workflows and only reads organization
		// ones.
		expect(
			canvas.getAllByRole("button", { name: "New workflow" }),
		).toHaveLength(1);
		expect(
			canvas.getByRole("button", {
				name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
			}),
		).toBeInTheDocument();
		expect(
			canvas.queryByRole("button", {
				name: `More actions for ${MockForkOrganizationSavedWorkflowMetadata.name}`,
			}),
		).toBeNull();
	},
};

export const Empty: Story = {
	parameters: {
		queries: workflowQueries(MockDefaultOrganization.id, [], []),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await canvas.findByText("No personal workflows in this organization yet.");
		await canvas.findByText("No organization workflows are available to you.");
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
	play: async ({ canvasElement }) => {
		await within(canvasElement).findByText(
			"Join an organization to save workflows.",
		);
	},
};
