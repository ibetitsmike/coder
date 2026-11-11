import type { Meta, StoryObj } from "@storybook/react-vite";
import { spyOn } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import { API } from "#/api/api";
import { infiniteChats } from "#/api/queries/chats";
import { agentTypes } from "#/api/queries/forkAgentDefinitions";
import type { Chat, ForkAgentType } from "#/api/typesGenerated";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockDefaultOrganization,
	MockUserMember,
	mockApiError,
} from "#/testHelpers/entities";
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
const chatsKey = infiniteChats(
	getSidebarChatListInput(DEFAULT_AGENT_SIDEBAR_FILTERS),
).queryKey;

const mockGeneralAgentType: ForkAgentType = {
	name: "general",
	source: "builtin",
	description: "A general purpose agent.",
	base_type: "general",
	root_available: true,
};

const mockReviewerAgentType: ForkAgentType = {
	name: "personal/reviewer",
	source: "personal",
	description: "Reviews the diff.",
	base_type: "general",
	root_available: false,
	root_unavailable_reason: "Its model is not available.",
};

const mockReviewChat: Chat = {
	...MockChat,
	title: "Review the login flow",
	agent_type: "reviewer",
	organization_id: MockDefaultOrganization.id,
};

const meta = {
	title: "pages/AgentsPage/ForkAvailableAgentsPage",
	component: ForkAvailableAgentsPage,
	decorators: [withAuthProvider, withDashboardProvider],
	parameters: {
		experiments: ["fork-agent-definitions"],
		user: { ...MockUserMember, organization_ids: [MockDefaultOrganization.id] },
		reactRouter: reactRouterParameters({
			location: { path: "/agents/settings/available-agents" },
			routing: { path: "/agents/settings/available-agents" },
		}),
	},
} satisfies Meta<typeof ForkAvailableAgentsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Loaded: Story = {
	parameters: {
		queries: [
			{ key: chatsKey, data: { pages: [[mockReviewChat]], pageParams: [0] } },
			{
				key: agentTypesKey,
				data: [mockGeneralAgentType, mockReviewerAgentType],
			},
		],
	},
};

export const NoAgents: Story = {
	parameters: {
		queries: [
			{ key: chatsKey, data: { pages: [[]], pageParams: [0] } },
			{ key: agentTypesKey, data: [] },
		],
	},
};

export const Loading: Story = {
	parameters: {
		queries: [{ key: chatsKey, data: { pages: [[]], pageParams: [0] } }],
	},
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "get").mockReturnValue(new Promise(() => {}));
	},
};

export const LoadError: Story = {
	parameters: {
		queries: [{ key: chatsKey, data: { pages: [[]], pageParams: [0] } }],
	},
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "get").mockRejectedValue(
			mockApiError({ message: "Failed to load the agent types." }),
		);
	},
};
