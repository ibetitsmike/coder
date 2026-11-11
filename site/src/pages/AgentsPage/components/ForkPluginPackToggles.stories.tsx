import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import { ForkPluginPackTogglesView } from "./ForkPluginPackToggles";

const meta = {
	title: "pages/AgentsPage/ForkPluginPackTogglesView",
	component: ForkPluginPackTogglesView,
	args: {
		installed: [MockForkPluginPack, MockForkPluginPackWithoutPath],
		enabled: [MockForkPluginPack],
		error: undefined,
		isRetrying: false,
		onRetry: fn(),
		canEdit: true,
		pending: undefined,
		onToggle: fn(),
	},
} satisfies Meta<typeof ForkPluginPackTogglesView>;

export default meta;
type Story = StoryObj<typeof ForkPluginPackTogglesView>;

export const Editable: Story = {};

export const ReadOnly: Story = {
	args: { canEdit: false },
};

export const Toggling: Story = {
	args: {
		pending: { pack: MockForkPluginPackWithoutPath, enabled: true },
	},
};

export const NoneInstalled: Story = {
	args: { installed: [], enabled: [] },
};

export const Loading: Story = {
	args: { enabled: undefined },
};

export const LoadError: Story = {
	args: {
		enabled: undefined,
		error: mockApiError({ message: "Failed to list plugin packs." }),
	},
};
