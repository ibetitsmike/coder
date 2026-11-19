import type { Meta, StoryObj } from "@storybook/react-vite";
import { type QueryClient, useQueryClient } from "react-query";
import { spyOn } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { API } from "#/api/api";
import { savedWorkflowsKey } from "#/api/queries/forkSavedWorkflows";
import { organizationsPermissions } from "#/api/queries/organizations";
import {
	MockDefaultOrganization,
	MockNoOrganizationPermissions,
	MockOrganization2,
	MockOrganizationPermissions,
	MockUserOwner,
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
import ForkWorkflowsPage from "./ForkWorkflowsPage";

const path = "/ai/settings/workflows";

const meta = {
	title: "pages/AISettingsPage/ForkWorkflowsPage",
	component: ForkWorkflowsPage,
	decorators: [withToaster, withAuthProvider, withDashboardProvider],
	parameters: {
		layout: "fullscreen",
		user: MockUserOwner,
		permissions: { viewAnyOrganizationSkills: true },
		experiments: ["fork-workflows"],
		organizations: [MockDefaultOrganization],
		reactRouter: reactRouterParameters({
			location: { path },
			routing: { path },
		}),
	},
} satisfies Meta<typeof ForkWorkflowsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Listed: Story = {
	parameters: {
		queries: [
			{
				key: organizationsPermissions([MockDefaultOrganization.id]).queryKey,
				data: { [MockDefaultOrganization.id]: MockOrganizationPermissions },
			},
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
				key: organizationsPermissions([MockDefaultOrganization.id]).queryKey,
				data: { [MockDefaultOrganization.id]: MockOrganizationPermissions },
			},
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

export const ReadOnly: Story = {
	parameters: {
		queries: [
			{
				key: organizationsPermissions([MockDefaultOrganization.id]).queryKey,
				data: {
					[MockDefaultOrganization.id]: {
						...MockNoOrganizationPermissions,
						viewOrganizationSkills: true,
					},
				},
			},
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

export const MultipleOrganizations: Story = {
	parameters: {
		user: {
			...MockUserOwner,
			organization_ids: [MockDefaultOrganization.id, MockOrganization2.id],
		},
		organizations: [MockDefaultOrganization, MockOrganization2],
		reactRouter: reactRouterParameters({
			location: { path, searchParams: { org: MockOrganization2.name } },
			routing: { path },
		}),
		queries: [
			{
				key: organizationsPermissions([
					MockDefaultOrganization.id,
					MockOrganization2.id,
				]).queryKey,
				data: {
					[MockDefaultOrganization.id]: MockOrganizationPermissions,
					[MockOrganization2.id]: MockOrganizationPermissions,
				},
			},
			{
				key: savedWorkflowsKey({
					tier: "user",
					organizationId: MockOrganization2.id,
				}),
				data: [MockForkSavedWorkflowMetadata],
			},
			{
				key: savedWorkflowsKey({
					tier: "organization",
					organizationId: MockOrganization2.id,
				}),
				data: [],
			},
		],
	},
};

// A user who reads another organization's workflows without being a member
// owns no personal workflows there, so only the organization tier shows.
export const NonMemberOrganization: Story = {
	parameters: {
		...MultipleOrganizations.parameters,
		user: MockUserOwner,
		queries: [
			{
				key: organizationsPermissions([
					MockDefaultOrganization.id,
					MockOrganization2.id,
				]).queryKey,
				data: {
					[MockDefaultOrganization.id]: MockOrganizationPermissions,
					[MockOrganization2.id]: MockOrganizationPermissions,
				},
			},
			{
				key: savedWorkflowsKey({
					tier: "organization",
					organizationId: MockOrganization2.id,
				}),
				data: [MockForkOrganizationSavedWorkflowMetadata],
			},
		],
	},
};

export const LoadingPermissions: Story = {
	beforeEach: () => {
		spyOn(API, "checkAuthorization").mockReturnValue(new Promise(() => {}));
	},
};

export const PermissionsError: Story = {
	beforeEach: () => {
		spyOn(API, "checkAuthorization").mockRejectedValue(
			new Error("Failed to load organization permissions."),
		);
	},
};

let capturedQueryClient: QueryClient | undefined;

export const PermissionsRefetchError: Story = {
	decorators: [
		(Story) => {
			capturedQueryClient = useQueryClient();
			return <Story />;
		},
	],
	parameters: {
		queries: [
			{
				key: organizationsPermissions([MockDefaultOrganization.id]).queryKey,
				data: { [MockDefaultOrganization.id]: MockOrganizationPermissions },
			},
		],
	},
	beforeEach: () => {
		spyOn(API, "checkAuthorization").mockRejectedValue(
			new Error("Failed to reload organization permissions."),
		);
	},
	play: async () => {
		if (!capturedQueryClient) {
			throw new Error("query client was not captured by the story decorator");
		}
		await capturedQueryClient.refetchQueries({
			queryKey: organizationsPermissions([MockDefaultOrganization.id]).queryKey,
			exact: true,
		});
	},
};
