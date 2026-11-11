import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, userEvent, within } from "storybook/test";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import { ForkPluginPacksPillView } from "./ForkPluginPacksPill";
import { ForkPluginPackTogglesView } from "./ForkPluginPackToggles";

const meta: Meta<typeof ForkPluginPacksPillView> = {
	title: "pages/AgentsPage/ForkPluginPacksPill",
	component: ForkPluginPacksPillView,
	args: {
		enabledCount: 1,
		children: (
			<ForkPluginPackTogglesView
				installed={[MockForkPluginPack, MockForkPluginPackWithoutPath]}
				enabled={[MockForkPluginPack]}
				error={undefined}
				isRetrying={false}
				onRetry={fn()}
				canEdit
				pending={undefined}
				onToggle={fn()}
			/>
		),
	},
};

export default meta;
type Story = StoryObj<typeof ForkPluginPacksPillView>;

export const Collapsed: Story = {};

export const NoneEnabled: Story = {
	args: { enabledCount: 0 },
};

export const Expanded: Story = {
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", { name: /Plugin packs/ }),
		);
	},
};
