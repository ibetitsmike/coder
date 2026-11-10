import type { Meta, StoryObj } from "@storybook/react-vite";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { userAgentDefinitionsKey } from "#/api/queries/forkAgentDefinitions";
import {
	MockDefaultOrganization,
	MockOrganization2,
	MockUserMember,
} from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import {
	withAuthProvider,
	withDashboardProvider,
} from "#/testHelpers/storybook";
import ForkAgentDefinitionsPage from "./ForkAgentDefinitionsPage";

const meta = {
	title: "pages/AgentsPage/ForkAgentDefinitionsPage",
	component: ForkAgentDefinitionsPage,
	decorators: [withAuthProvider, withDashboardProvider],
	parameters: {
		experiments: ["fork-agent-definitions"],
		reactRouter: reactRouterParameters({
			location: { path: "/agents/settings/agent-definitions" },
			routing: { path: "/agents/settings/agent-definitions" },
		}),
	},
} satisfies Meta<typeof ForkAgentDefinitionsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const MultipleOrganizations: Story = {
	parameters: {
		user: {
			...MockUserMember,
			organization_ids: [MockDefaultOrganization.id, MockOrganization2.id],
		},
		organizations: [MockDefaultOrganization, MockOrganization2],
		queries: [
			{
				key: userAgentDefinitionsKey(MockDefaultOrganization.id),
				data: [MockForkAgentDefinitionMetadata],
			},
		],
	},
};

export const NoOrganization: Story = {
	parameters: {
		user: { ...MockUserMember, organization_ids: [] },
		organizations: [MockDefaultOrganization],
	},
};
