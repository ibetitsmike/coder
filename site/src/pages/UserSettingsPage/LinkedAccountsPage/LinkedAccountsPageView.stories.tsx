import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import type { LinkablePlugin } from "#/api/typesGenerated";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockLinkablePlugin,
	MockPluginConnectPrompt,
	MockPluginLinkCode,
	MockPluginUserLink,
} from "#/testHelpers/plugins";
import { LinkedAccountsPageView } from "./LinkedAccountsPageView";

const mockPluginWithoutLinks: LinkablePlugin = {
	...MockLinkablePlugin,
	id: "plugin-matrix",
	name: "matrix-bridge",
	version: "0.3.1",
	links: [],
};

const mockPluginWithTwoLinks: LinkablePlugin = {
	...MockLinkablePlugin,
	links: [
		MockPluginUserLink,
		{
			...MockPluginUserLink,
			external_id: "+15550100002",
			created_at: "2024-01-02T09:30:00Z",
		},
	],
};

const mockConnectPlugin: LinkablePlugin = {
	...MockLinkablePlugin,
	connect: true,
	links: [],
};

const meta: Meta<typeof LinkedAccountsPageView> = {
	title: "pages/UserSettingsPage/LinkedAccountsPageView",
	component: LinkedAccountsPageView,
	args: {
		plugins: [mockPluginWithTwoLinks, mockPluginWithoutLinks],
		linkCodes: {},
		now: new Date("2024-01-01T00:01:00Z"),
		onCreateLinkCode: fn(),
		onUnlink: fn(),
	},
};

export default meta;
type Story = StoryObj<typeof LinkedAccountsPageView>;

export const WithLinks: Story = {};

export const WithoutLinks: Story = {
	args: {
		plugins: [mockPluginWithoutLinks],
	},
};

export const LinkCodeShown: Story = {
	args: {
		linkCodes: { [MockLinkablePlugin.id]: { linkCode: MockPluginLinkCode } },
	},
};

export const LinkCodeExpired: Story = {
	args: {
		linkCodes: { [MockLinkablePlugin.id]: { linkCode: MockPluginLinkCode } },
		now: new Date(MockPluginLinkCode.expires_at),
	},
};

export const ConnectWithoutLinks: Story = {
	args: {
		plugins: [mockConnectPlugin],
	},
};

export const ConnectPromptShown: Story = {
	args: {
		plugins: [mockConnectPlugin],
		linkCodes: {
			[mockConnectPlugin.id]: {
				linkCode: MockPluginLinkCode,
				connectPrompt: MockPluginConnectPrompt,
			},
		},
	},
};

export const ConnectPromptWithoutQRCode: Story = {
	args: {
		plugins: [mockConnectPlugin],
		linkCodes: {
			[mockConnectPlugin.id]: {
				linkCode: MockPluginLinkCode,
				connectPrompt: { ...MockPluginConnectPrompt, qr_code_png: undefined },
			},
		},
	},
};

export const ConnectPromptWithoutInstructions: Story = {
	args: {
		plugins: [mockConnectPlugin],
		linkCodes: {
			[mockConnectPlugin.id]: {
				linkCode: MockPluginLinkCode,
				connectPrompt: { ...MockPluginConnectPrompt, instructions: "" },
			},
		},
	},
};

export const ConnectPromptExpired: Story = {
	args: {
		...ConnectPromptShown.args,
		now: new Date(MockPluginLinkCode.expires_at),
	},
};

export const CreatingLinkCode: Story = {
	args: {
		creatingLinkCodePluginId: MockLinkablePlugin.id,
	},
};

export const NoPlugins: Story = {
	args: {
		plugins: [],
	},
};

export const Loading: Story = {
	args: {
		plugins: undefined,
	},
};

export const LoadError: Story = {
	args: {
		plugins: undefined,
		getPluginsError: mockApiError({
			message: "Failed to load plugins.",
		}),
	},
};

export const RefetchError: Story = {
	args: {
		getPluginsError: mockApiError({
			message: "Failed to refresh plugins.",
		}),
	},
};
