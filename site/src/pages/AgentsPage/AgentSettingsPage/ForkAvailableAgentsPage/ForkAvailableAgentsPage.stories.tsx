import type { Meta, StoryObj } from "@storybook/react-vite";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { infiniteChats } from "#/api/queries/chats";
import { agentTypes } from "#/api/queries/forkAgentDefinitions";
import { MockChat } from "#/testHelpers/chatEntities";
import { MockDefaultOrganization } from "#/testHelpers/entities";
import {
	withAuthProvider,
	withDashboardProvider,
} from "#/testHelpers/storybook";
import {
	DEFAULT_AGENT_SIDEBAR_FILTERS,
	getSidebarChatListInput,
} from "../../utils/agentSidebarFilters";
import ForkAvailableAgentsPage from "./ForkAvailableAgentsPage";

const agentTypesKey = agentTypes(MockDefaultOrganization.id, {}).queryKey;

const meta = {
	title: "pages/AgentsPage/ForkAvailableAgentsPage",
	component: ForkAvailableAgentsPage,
	decorators: [withAuthProvider, withDashboardProvider],
	parameters: {
		experiments: ["fork-agent-definitions"],
		reactRouter: reactRouterParameters({
			location: { path: "/agents/settings/available-agents" },
			routing: { path: "/agents/settings/available-agents" },
		}),
	},
} satisfies Meta<typeof ForkAvailableAgentsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

const chatsQuery = {
	key: infiniteChats(getSidebarChatListInput(DEFAULT_AGENT_SIDEBAR_FILTERS))
		.queryKey,
	data: {
		pages: [
			[
				{
					...MockChat,
					title: "Review the login flow",
					agent_type: "reviewer",
					organization_id: MockDefaultOrganization.id,
				},
			],
		],
		pageParams: [0],
	},
};

export const Loaded: Story = {
	parameters: {
		queries: [
			chatsQuery,
			{
				key: agentTypesKey,
				data: [
					{
						name: "general",
						source: "builtin",
						description: "A general purpose agent.",
						base_type: "general",
						root_available: true,
					},
					{
						name: "personal/reviewer",
						source: "personal",
						description: "Reviews the diff.",
						base_type: "general",
						root_available: false,
						root_unavailable_reason: "Its model is not available.",
					},
				],
			},
		],
	},
};

export const NoAgents: Story = {
	parameters: { queries: [chatsQuery, { key: agentTypesKey, data: [] }] },
};
