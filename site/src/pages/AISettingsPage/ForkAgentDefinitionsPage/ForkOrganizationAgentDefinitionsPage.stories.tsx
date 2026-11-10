import type { Meta, StoryObj } from "@storybook/react-vite";
import { type QueryClient, useQueryClient } from "react-query";
import { spyOn } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { API } from "#/api/api";
import { agentDefinitionsKey } from "#/api/queries/forkAgentDefinitions";
import { organizationsPermissions } from "#/api/queries/organizations";
import type { ForkAgentDefinitionMetadata } from "#/api/typesGenerated";
import type { OrganizationPermissions } from "#/modules/permissions/organizations";
import {
	MockDefaultOrganization,
	MockNoOrganizationPermissions,
	MockOrganization2,
	MockOrganizationPermissions,
	MockUserOwner,
} from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import {
	withAuthProvider,
	withDashboardProvider,
	withToaster,
} from "#/testHelpers/storybook";
import ForkOrganizationAgentDefinitionsPage from "./ForkOrganizationAgentDefinitionsPage";

const path = "/ai/settings/agent-definitions";

const defaultOrganizationPermissions = (
	permissions: OrganizationPermissions,
) => ({
	key: organizationsPermissions([MockDefaultOrganization.id]).queryKey,
	data: { [MockDefaultOrganization.id]: permissions },
});

const defaultOrganizationDefinitions = (
	data: readonly ForkAgentDefinitionMetadata[],
) => ({
	key: agentDefinitionsKey({
		tier: "organization",
		organizationId: MockDefaultOrganization.id,
	}),
	data,
});

const meta = {
	title: "pages/AISettingsPage/ForkOrganizationAgentDefinitionsPage",
	component: ForkOrganizationAgentDefinitionsPage,
	decorators: [withToaster, withAuthProvider, withDashboardProvider],
	parameters: {
		layout: "fullscreen",
		user: MockUserOwner,
		permissions: { viewAnyOrganizationSkills: true },
		experiments: ["fork-agent-definitions"],
		organizations: [MockDefaultOrganization],
		reactRouter: reactRouterParameters({
			location: { path },
			routing: { path },
		}),
	},
} satisfies Meta<typeof ForkOrganizationAgentDefinitionsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const OrganizationAdmin: Story = {
	parameters: {
		queries: [
			defaultOrganizationPermissions(MockOrganizationPermissions),
			defaultOrganizationDefinitions([MockForkAgentDefinitionMetadata]),
		],
	},
};

export const Empty: Story = {
	parameters: {
		queries: [
			defaultOrganizationPermissions(MockOrganizationPermissions),
			defaultOrganizationDefinitions([]),
		],
	},
};

export const ReadOnly: Story = {
	parameters: {
		queries: [
			defaultOrganizationPermissions({
				...MockNoOrganizationPermissions,
				viewOrganizationSkills: true,
			}),
			defaultOrganizationDefinitions([MockForkAgentDefinitionMetadata]),
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
			defaultOrganizationPermissions(MockOrganizationPermissions),
			defaultOrganizationDefinitions([MockForkAgentDefinitionMetadata]),
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

const secondOrganizationRoute = reactRouterParameters({
	location: { path, searchParams: { org: MockOrganization2.name } },
	routing: { path },
});

export const MultipleOrganizations: Story = {
	parameters: {
		organizations: [MockDefaultOrganization, MockOrganization2],
		reactRouter: secondOrganizationRoute,
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
				key: agentDefinitionsKey({
					tier: "organization",
					organizationId: MockOrganization2.id,
				}),
				data: [MockForkAgentDefinitionMetadata],
			},
		],
	},
};

export const DeniedOrganization: Story = {
	parameters: {
		organizations: [MockDefaultOrganization, MockOrganization2],
		reactRouter: secondOrganizationRoute,
		queries: [
			{
				key: organizationsPermissions([
					MockDefaultOrganization.id,
					MockOrganization2.id,
				]).queryKey,
				data: {
					[MockDefaultOrganization.id]: MockOrganizationPermissions,
					[MockOrganization2.id]: MockNoOrganizationPermissions,
				},
			},
		],
	},
};
