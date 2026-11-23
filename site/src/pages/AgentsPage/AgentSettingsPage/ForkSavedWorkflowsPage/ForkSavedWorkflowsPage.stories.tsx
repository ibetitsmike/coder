import type { Meta, StoryObj } from "@storybook/react-vite";
import { screen, userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { savedWorkflowsKey } from "#/api/queries/forkSavedWorkflows";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockUserMember,
} from "#/testHelpers/entities";
import {
	MockForkDeleteOnlySavedWorkflowMetadata,
	MockForkEditableOrganizationSavedWorkflowMetadata,
	MockForkLockedSavedWorkflowMetadata,
	MockForkOrganizationSavedWorkflowMetadata,
	MockForkSavedWorkflowMetadata,
	MockForkSharedSavedWorkflowMetadata,
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

export const SharedByMe: Story = {
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
				data: [
					MockForkOrganizationSavedWorkflowMetadata,
					MockForkSharedSavedWorkflowMetadata,
				],
			},
		],
	},
};

/** The server's permissions decide each row's actions. */
export const PermissionRows: Story = {
	parameters: {
		queries: [
			{
				key: savedWorkflowsKey({
					tier: "user",
					organizationId: MockDefaultOrganization.id,
				}),
				data: [
					MockForkSavedWorkflowMetadata,
					MockForkDeleteOnlySavedWorkflowMetadata,
					MockForkLockedSavedWorkflowMetadata,
				],
			},
			{
				key: savedWorkflowsKey({
					tier: "organization",
					organizationId: MockDefaultOrganization.id,
				}),
				data: [MockForkEditableOrganizationSavedWorkflowMetadata],
			},
		],
	},
};

/** The menu of a personal row with every right. */
export const PermissionRowsPersonalMenu: Story = {
	parameters: PermissionRows.parameters,
	play: async () => {
		const personal = await screen.findByRole("table", {
			name: "Personal workflows",
		});
		await userEvent.click(
			await within(personal).findByRole("button", {
				name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
			}),
		);
		await screen.findByRole("menuitem", { name: "Delete\u2026" });
	},
};

/** The menu of a personal row with only the delete right. */
export const PermissionRowsDeleteOnlyMenu: Story = {
	parameters: PermissionRows.parameters,
	play: async () => {
		const personal = await screen.findByRole("table", {
			name: "Personal workflows",
		});
		await userEvent.click(
			await within(personal).findByRole("button", {
				name: `More actions for ${MockForkDeleteOnlySavedWorkflowMetadata.name}`,
			}),
		);
		await screen.findByRole("menuitem", { name: "Delete\u2026" });
	},
};

/** The menu of an editable organization row. */
export const PermissionRowsOrganizationMenu: Story = {
	parameters: PermissionRows.parameters,
	play: async () => {
		const organization = await screen.findByRole("table", {
			name: "Organization workflows",
		});
		await userEvent.click(
			await within(organization).findByRole("button", {
				name: `More actions for ${MockForkEditableOrganizationSavedWorkflowMetadata.name}`,
			}),
		);
		await screen.findByRole("menuitem", { name: "Delete\u2026" });
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
