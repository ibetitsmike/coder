import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
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
		error: undefined,
		isLoading: false,
		isRetrying: false,
		onRetry: fn(),
		onToggleEnabled: fn(),
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

export const DeleteConfirm: Story = {
	args: {
		deleteState: {
			definition: MockForkAgentDefinitionMetadata,
			isDeleting: false,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const DeleteFailed: Story = {
	args: {
		deleteState: {
			definition: MockForkAgentDefinitionMetadata,
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
