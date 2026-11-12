import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import {
	availablePluginPacksKey,
	enabledPluginPacksKey,
} from "#/api/queries/forkPluginPackEnablement";
import { MockChatProject } from "#/testHelpers/entities";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import { ForkProjectPluginPacksDialog } from "./ForkProjectPluginPacksDialog";

const meta: Meta<typeof ForkProjectPluginPacksDialog> = {
	title: "pages/AgentsPage/ProjectPage/ForkProjectPluginPacksDialog",
	component: ForkProjectPluginPacksDialog,
	args: {
		project: MockChatProject,
		open: true,
		onOpenChange: fn(),
		onCloseAutoFocus: fn(),
	},
};

export default meta;
type Story = StoryObj<typeof ForkProjectPluginPacksDialog>;

export const Editable: Story = {
	parameters: {
		queries: [
			{
				key: enabledPluginPacksKey({
					kind: "project",
					organizationId: MockChatProject.organization_id,
					projectId: MockChatProject.id,
				}),
				data: [MockForkPluginPack],
			},
			{
				key: availablePluginPacksKey(MockChatProject.organization_id),
				data: [MockForkPluginPack, MockForkPluginPackWithoutPath],
			},
		],
	},
};

export const ReadOnly: Story = {
	args: {
		project: {
			...MockChatProject,
			permissions: { ...MockChatProject.permissions, update: false },
		},
	},
	parameters: {
		queries: [
			{
				key: enabledPluginPacksKey({
					kind: "project",
					organizationId: MockChatProject.organization_id,
					projectId: MockChatProject.id,
				}),
				data: [MockForkPluginPack],
			},
			{
				key: availablePluginPacksKey(MockChatProject.organization_id),
				data: [MockForkPluginPack, MockForkPluginPackWithoutPath],
			},
		],
	},
};
