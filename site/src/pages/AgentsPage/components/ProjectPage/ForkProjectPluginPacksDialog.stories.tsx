import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import {
	availablePluginPacks,
	enabledPluginPacks,
} from "#/api/queries/forkPluginPackEnablement";
import { MockChatProject } from "#/testHelpers/entities";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import { ForkProjectPluginPacksDialog } from "./ForkProjectPluginPacksDialog";

const enabledKey = enabledPluginPacks({
	kind: "project",
	organizationId: MockChatProject.organization_id,
	projectId: MockChatProject.id,
}).queryKey;
const installedKey = availablePluginPacks(
	MockChatProject.organization_id,
).queryKey;

const meta: Meta<typeof ForkProjectPluginPacksDialog> = {
	title: "pages/AgentsPage/ProjectPage/ForkProjectPluginPacksDialog",
	component: ForkProjectPluginPacksDialog,
	args: {
		project: MockChatProject,
		open: true,
		onOpenChange: fn(),
		onCloseAutoFocus: fn(),
	},
	parameters: {
		queries: [
			{ key: enabledKey, data: [MockForkPluginPack] },
			{
				key: installedKey,
				data: [MockForkPluginPack, MockForkPluginPackWithoutPath],
			},
		],
	},
};

export default meta;
type Story = StoryObj<typeof ForkProjectPluginPacksDialog>;

export const Editable: Story = {};

export const ReadOnly: Story = {
	args: {
		project: {
			...MockChatProject,
			permissions: { ...MockChatProject.permissions, update: false },
		},
	},
};
