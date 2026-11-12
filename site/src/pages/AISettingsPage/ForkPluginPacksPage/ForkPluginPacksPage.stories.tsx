import type { Meta, StoryObj } from "@storybook/react-vite";
import { type QueryClient, useQueryClient } from "react-query";
import { spyOn } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { API } from "#/api/api";
import { pluginPacksKey } from "#/api/queries/forkPluginPacks";
import { organizationsPermissions } from "#/api/queries/organizations";
import {
	MockDefaultOrganization,
	MockNoOrganizationPermissions,
	MockOrganization2,
	MockOrganizationPermissions,
	MockUserOwner,
} from "#/testHelpers/entities";
import { MockForkPluginPack } from "#/testHelpers/forkPluginPacks";
import {
	withAuthProvider,
	withDashboardProvider,
	withToaster,
} from "#/testHelpers/storybook";
import ForkPluginPacksPage from "./ForkPluginPacksPage";

const path = "/ai/settings/plugin-packs";

const meta = {
	title: "pages/AISettingsPage/ForkPluginPacksPage",
	component: ForkPluginPacksPage,
	decorators: [withToaster, withAuthProvider, withDashboardProvider],
	parameters: {
		layout: "fullscreen",
		user: MockUserOwner,
		permissions: { viewAnyOrganizationSkills: true },
		experiments: ["fork-plugin-packs"],
		organizations: [MockDefaultOrganization],
		reactRouter: reactRouterParameters({
			location: { path },
			routing: { path },
		}),
	},
} satisfies Meta<typeof ForkPluginPacksPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Populated: Story = {
	parameters: {
		queries: [
			{
				key: organizationsPermissions([MockDefaultOrganization.id]).queryKey,
				data: { [MockDefaultOrganization.id]: MockOrganizationPermissions },
			},
			{
				key: pluginPacksKey(MockDefaultOrganization.id),
				data: [MockForkPluginPack],
			},
		],
	},
};

export const NoSkillsPermission: Story = {
	parameters: {
		permissions: { viewAnyOrganizationSkills: false },
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
			{
				key: pluginPacksKey(MockDefaultOrganization.id),
				data: [MockForkPluginPack],
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

export const DeniedOrganization: Story = {
	parameters: {
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
					[MockOrganization2.id]: MockNoOrganizationPermissions,
				},
			},
		],
	},
};
