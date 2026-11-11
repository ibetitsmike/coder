import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { agentDefinitionsKey } from "#/api/queries/forkAgentDefinitions";
import { MockChatProject } from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { ProjectAgentDefinitionsDialog } from "./ProjectAgentDefinitionsDialog";

const projectScopeKey = agentDefinitionsKey({
	tier: "project",
	organizationId: MockChatProject.organization_id,
	projectId: MockChatProject.id,
});

const meta: Meta<typeof ProjectAgentDefinitionsDialog> = {
	title: "pages/AgentsPage/ProjectPage/ProjectAgentDefinitionsDialog",
	component: ProjectAgentDefinitionsDialog,
	args: {
		project: MockChatProject,
		open: true,
		onOpenChange: fn(),
		onCloseAutoFocus: fn(),
		canEdit: true,
	},
	parameters: {
		queries: [
			{
				key: projectScopeKey,
				data: [
					MockForkAgentDefinitionMetadata,
					{
						...MockForkAgentDefinitionMetadata,
						id: "agent-definition-2",
						name: "planner",
						description: "Breaks a task into reviewable steps.",
						enabled: false,
					},
				],
			},
		],
	},
};

export default meta;
type Story = StoryObj<typeof ProjectAgentDefinitionsDialog>;

export const List: Story = {};

export const ReadOnly: Story = {
	args: { canEdit: false },
};

export const Empty: Story = {
	parameters: {
		queries: [{ key: projectScopeKey, data: [] }],
	},
};
