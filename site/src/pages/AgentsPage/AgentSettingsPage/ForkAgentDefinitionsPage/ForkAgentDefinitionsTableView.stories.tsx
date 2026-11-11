import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, userEvent, within } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { readOnlySkillAccess } from "../../utils/skills";
import { emptyAgentDefinitionFormValues } from "./agentDefinitionMarkdown";
import { ForkAgentDefinitionsTableView } from "./ForkAgentDefinitionsTableView";

const MockReleaseNotesDefinition = {
	...MockForkAgentDefinitionMetadata,
	id: "agent-definition-release-notes",
	name: "release-notes",
	description: "",
	enabled: false,
};

const meta = {
	title: "pages/AgentsPage/ForkAgentDefinitionsTableView",
	component: ForkAgentDefinitionsTableView,
	args: {
		definitions: [MockForkAgentDefinitionMetadata, MockReleaseNotesDefinition],
		description:
			"Custom agent types your chats can spawn with spawn_agent in this organization.",
		emptyMessage: "No personal agent definitions in this organization yet.",
		error: undefined,
		isLoading: false,
		isRetrying: false,
		onRetry: fn(),
		onToggleEnabled: fn(),
		onCreate: fn(),
		onEdit: fn(),
		onDelete: fn(),
	},
} satisfies Meta<typeof ForkAgentDefinitionsTableView>;

export default meta;
type Story = StoryObj<typeof ForkAgentDefinitionsTableView>;

export const Populated: Story = {};

export const Loading: Story = {
	args: { definitions: [], isLoading: true },
};

export const LoadError: Story = {
	args: {
		definitions: [],
		error: mockApiError({
			message: "Failed to list agent definitions.",
			detail: "permission denied",
		}),
	},
};

export const Empty: Story = {
	args: { definitions: [] },
};

export const EmptyReadOnly: Story = {
	args: { definitions: [], access: readOnlySkillAccess },
};

export const TogglesPending: Story = {
	args: {
		pendingToggles: {
			[MockForkAgentDefinitionMetadata.name]:
				!MockForkAgentDefinitionMetadata.enabled,
			[MockReleaseNotesDefinition.name]: !MockReleaseNotesDefinition.enabled,
		},
	},
};

export const RowActionsOpen: Story = {
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", {
				name: `More actions for ${MockForkAgentDefinitionMetadata.name}`,
			}),
		);
	},
};

export const EditorOpen: Story = {
	args: {
		editorState: {
			mode: "create",
			initialValues: emptyAgentDefinitionFormValues,
			isSubmitting: false,
			onSubmit: fn(),
			onClose: fn(),
		},
	},
};

export const ReadOnly: Story = {
	args: { access: readOnlySkillAccess },
};

const deleteMessage = `Your personal definition ${MockForkAgentDefinitionMetadata.name} will be deleted. Organization, project, and workspace definitions with the same name are not affected. This action cannot be undone.`;

export const DeleteConfirm: Story = {
	args: {
		deleteState: {
			definition: MockForkAgentDefinitionMetadata,
			message: deleteMessage,
			isDeleting: false,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const DeletePending: Story = {
	args: {
		deleteState: {
			definition: MockForkAgentDefinitionMetadata,
			message: deleteMessage,
			isDeleting: true,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const DeleteFailed: Story = {
	args: {
		deleteState: {
			definition: MockForkAgentDefinitionMetadata,
			message: deleteMessage,
			error: mockApiError({
				message: "Agent definition not found.",
				detail: "It may have been deleted in another tab.",
			}),
			isDeleting: false,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const RefreshError: Story = {
	args: {
		error: mockApiError({
			message: "Failed to list agent definitions.",
			detail: "connection reset",
		}),
	},
};

export const RetryingRefresh: Story = {
	args: {
		isRetrying: true,
		error: mockApiError({
			message: "Failed to list agent definitions.",
			detail: "connection reset",
		}),
	},
};
