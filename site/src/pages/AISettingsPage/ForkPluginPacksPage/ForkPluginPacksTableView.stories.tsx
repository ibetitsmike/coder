import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import { ForkPluginPacksTableView } from "./ForkPluginPacksTableView";

const meta = {
	title: "pages/AISettingsPage/ForkPluginPacksTableView",
	component: ForkPluginPacksTableView,
	args: {
		packs: [MockForkPluginPack, MockForkPluginPackWithoutPath],
		error: undefined,
		isLoading: false,
		isRetrying: false,
		onRetry: fn(),
	},
} satisfies Meta<typeof ForkPluginPacksTableView>;

export default meta;
type Story = StoryObj<typeof ForkPluginPacksTableView>;

export const Populated: Story = {};

export const Empty: Story = {
	args: { packs: [] },
};

export const LoadError: Story = {
	args: {
		packs: [],
		error: mockApiError({ message: "Failed to list plugin packs." }),
	},
};

export const Loading: Story = {
	args: { packs: [], isLoading: true },
};

export const RefetchError: Story = {
	args: {
		error: mockApiError({ message: "Failed to list plugin packs." }),
	},
};
