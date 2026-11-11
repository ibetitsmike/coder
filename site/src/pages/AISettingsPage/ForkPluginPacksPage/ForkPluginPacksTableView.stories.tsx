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
		access: { create: true, update: true, delete: true },
		error: undefined,
		isLoading: false,
		isRetrying: false,
		onRetry: fn(),
		onInstall: fn(),
		onUpdate: fn(),
		onDelete: fn(),
	},
} satisfies Meta<typeof ForkPluginPacksTableView>;

export default meta;
type Story = StoryObj<typeof ForkPluginPacksTableView>;

export const Populated: Story = {};

export const ReadOnly: Story = {
	args: { access: { create: false, update: false, delete: false } },
};

export const UpdateOnly: Story = {
	args: { access: { create: false, update: true, delete: false } },
};

export const Updating: Story = {
	args: { updatingPackId: MockForkPluginPack.id },
};

export const Empty: Story = {
	args: { packs: [] },
};

export const EmptyReadOnly: Story = {
	args: {
		packs: [],
		access: { create: false, update: false, delete: false },
	},
};

export const Loading: Story = {
	args: { packs: [], isLoading: true },
};

export const LoadError: Story = {
	args: {
		packs: [],
		error: mockApiError({ message: "Failed to list plugin packs." }),
	},
};

export const Retrying: Story = {
	args: {
		packs: [],
		error: mockApiError({ message: "Failed to list plugin packs." }),
		isRetrying: true,
	},
};

export const RefetchError: Story = {
	args: {
		error: mockApiError({ message: "Failed to list plugin packs." }),
	},
};
